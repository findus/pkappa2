package converters

import (
	"bufio"
	"encoding/binary"
	"io"
	"os"
	"sync"
	"unsafe"

	"github.com/spq/pkappa2/internal/index"
)

type (
	cacheFile struct {
		file      *os.File
		cachePath string
		rwmutex   sync.RWMutex
		fileSize  int64

		// Map of streamid to file offset in the cache file.
		containedStreamIds map[uint64]streamInfo
	}

	streamInfo struct {
		offset int64
		size   uint64
	}

	// File format
	converterStreamSection struct {
		StreamID uint64
	}
)

const (
	InvalidStreamID = ^uint64(0)
)

func NewCacheFile(cachePath string) (*cacheFile, error) {
	file, err := os.OpenFile(cachePath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}

	// Read all stream ids
	containedStreamIds := make(map[uint64]streamInfo)
	buffer := bufio.NewReader(file)
	offset := int64(0)
	for {
		streamSection := converterStreamSection{}
		if err := binary.Read(buffer, binary.LittleEndian, &streamSection); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		offset += int64(unsafe.Sizeof(streamSection))

		// Read total data size of the stream by adding all chunk sizes up.
		prevWasZero := false
		dataSize := uint64(0)
		for {
			sz := uint64(0)
			for {
				b, err := buffer.ReadByte()
				if err != nil {
					return nil, err
				}
				dataSize++
				sz <<= 7
				sz |= uint64(b & 0x7f)
				if b < 0x80 {
					break
				}
			}
			if sz == 0 && prevWasZero {
				break
			}
			dataSize += sz
			prevWasZero = sz == 0
		}

		if streamSection.StreamID != InvalidStreamID {
			containedStreamIds[streamSection.StreamID] = streamInfo{
				offset: offset,
				size:   dataSize,
			}
		}
		if _, err := buffer.Discard(int(dataSize)); err != nil {
			return nil, err
		}
		offset += int64(dataSize)
	}

	// Keep the file pointer at the end of the file.
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return nil, err
	}

	return &cacheFile{
		file:               file,
		cachePath:          cachePath,
		containedStreamIds: containedStreamIds,
		fileSize:           offset,
	}, nil
}

func (cachefile *cacheFile) Reset() error {
	cachefile.rwmutex.Lock()
	defer cachefile.rwmutex.Unlock()

	if err := cachefile.file.Truncate(0); err != nil {
		return err
	}
	cachefile.containedStreamIds = make(map[uint64]streamInfo)
	cachefile.fileSize = 0
	return nil
}

func (cachefile *cacheFile) Data(streamID uint64) ([]index.Data, uint64, uint64, error) {
	cachefile.rwmutex.RLock()
	defer cachefile.rwmutex.RUnlock()

	info, ok := cachefile.containedStreamIds[streamID]
	if !ok {
		return nil, 0, 0, nil
	}

	// [u64 stream id] [u8 varint chunk sizes] [client data] [server data]
	buffer := bufio.NewReader(io.NewSectionReader(cachefile.file, info.offset, int64(info.size)))
	data := []index.Data{}

	type sizeAndDirection struct {
		Size      uint64
		Direction index.Direction
	}
	// Read chunk sizes
	dataSizes := []sizeAndDirection{}
	prevWasZero := false
	direction := index.DirectionClientToServer
	clientBytes := uint64(0)
	serverBytes := uint64(0)
	for {
		sz := uint64(0)
		for {
			b, err := buffer.ReadByte()
			if err != nil {
				return nil, 0, 0, err
			}
			sz <<= 7
			sz |= uint64(b & 0x7f)
			if b < 0x80 {
				break
			}
		}
		if sz == 0 && prevWasZero {
			break
		}
		dataSizes = append(dataSizes, sizeAndDirection{Direction: direction, Size: sz})
		prevWasZero = sz == 0
		if direction == index.DirectionClientToServer {
			clientBytes += sz
		} else {
			serverBytes += sz
		}
		direction = direction.Reverse()
	}

	// Read data
	clientData := make([]byte, clientBytes)
	if _, err := io.ReadFull(buffer, clientData); err != nil {
		return nil, 0, 0, err
	}
	serverData := make([]byte, serverBytes)
	if _, err := io.ReadFull(buffer, serverData); err != nil {
		return nil, 0, 0, err
	}

	// Split data into chunks
	for _, ds := range dataSizes {
		if ds.Size == 0 {
			continue
		}
		var bytes []byte
		if ds.Direction == index.DirectionClientToServer {
			bytes = clientData[:ds.Size]
			clientData = clientData[ds.Size:]
		} else {
			bytes = serverData[:ds.Size]
			serverData = serverData[ds.Size:]
		}
		data = append(data, index.Data{
			Direction: ds.Direction,
			Content:   bytes,
		})
	}
	return data, clientBytes, serverBytes, nil
}

func (cachefile *cacheFile) SetData(streamID uint64, convertedPackets []index.Data) error {
	cachefile.rwmutex.Lock()
	defer cachefile.rwmutex.Unlock()

	writer := bufio.NewWriter(cachefile.file)
	// [u64 stream id] [u8 varint chunk sizes] [client data] [server data]
	// Write stream header
	streamSection := converterStreamSection{
		StreamID: streamID,
	}
	if err := binary.Write(writer, binary.LittleEndian, &streamSection); err != nil {
		return err
	}

	streamSize := uint64(0)
	buf := [10]byte{}
	for pIndex, wantDir := 0, index.DirectionClientToServer; pIndex < len(convertedPackets); {
		// TODO: Merge packets with the same direction. Do we even want to allow converters to change the direction?
		convertedPacket := convertedPackets[pIndex]
		sz := len(convertedPacket.Content)
		dir := convertedPacket.Direction
		// Write a length of 0 if the server sent the first packet.
		if dir != wantDir {
			if err := writer.WriteByte(0); err != nil {
				return err
			}
			streamSize++
			wantDir = wantDir.Reverse()
		}
		pos := len(buf)
		flag := byte(0)
		for {
			pos--
			streamSize++
			buf[pos] = byte(sz&0x7f) | flag
			flag = 0x80
			sz >>= 7
			if sz == 0 {
				break
			}
		}
		if err := binary.Write(writer, binary.LittleEndian, buf[pos:]); err != nil {
			return err
		}
		wantDir = wantDir.Reverse()
		pIndex++
	}
	// Append two lengths of 0 to indicate the end of the chunk sizes
	if err := binary.Write(writer, binary.LittleEndian, []byte{0, 0}); err != nil {
		// TODO: The cache file is corrupt now. We should probably delete it.
		return err
	}
	streamSize += 2

	// Write chunk data
	for _, direction := range []index.Direction{index.DirectionClientToServer, index.DirectionServerToClient} {
		for _, convertedPacket := range convertedPackets {
			if convertedPacket.Direction != direction {
				continue
			}
			if err := binary.Write(writer, binary.LittleEndian, convertedPacket.Content); err != nil {
				return err
			}
			streamSize += uint64(len(convertedPacket.Content))
		}
	}

	if err := writer.Flush(); err != nil {
		return err
	}

	// Remember where to look for this stream.
	cachefile.containedStreamIds[streamID] = streamInfo{
		offset: cachefile.fileSize + int64(unsafe.Sizeof(streamSection)),
		size:   streamSize,
	}

	cachefile.fileSize += int64(unsafe.Sizeof(streamSection)) + int64(streamSize)

	return nil
}

// func (writer *writer) invalidateStream(stream *index.Stream) error {

// 	offset, ok := writer.cache.containedStreamIds[stream.ID()]
// 	if !ok {
// 		return nil
// 	}

// 	if err := writer.buffer.Flush(); err != nil {
// 		return err
// 	}
// 	if _, err := writer.file.Seek(offset, io.SeekStart); err != nil {
// 		return err
// 	}

// 	// Find stream in file and replace streamid with InvalidStreamID
// 	streamSection := converterStreamSection{}
// 	if err := binary.Read(writer.file, binary.LittleEndian, &streamSection); err != nil {
// 		return err
// 	}
// 	// Should never happen
// 	if streamSection.StreamID != stream.ID() {
// 		return fmt.Errorf("stream id mismatch during invalidation: %d != %d, offset %d", streamSection.StreamID, stream.ID(), offset)
// 	}

// 	streamSection.StreamID = InvalidStreamID
// 	if _, err := writer.file.Seek(-int64(unsafe.Sizeof(streamSection)), io.SeekCurrent); err != nil {
// 		return err
// 	}
// 	if err := binary.Write(writer.file, binary.LittleEndian, streamSection); err != nil {
// 		return err
// 	}

// 	delete(writer.cache.containedStreamIds, stream.ID())
// 	return nil
// }
