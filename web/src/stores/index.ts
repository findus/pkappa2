import { defineStore } from "pinia";
import axios from "axios";
import { useStreamsStore } from "./streams";
import { useStreamStore } from "./stream";
import { setupWebsocket } from "./websocket";
import APIClient, {
  ConverterStatistics,
  PcapInfo,
  PcapOverIPEndpoint,
  Statistics,
  TagInfo,
  ClientConfig,
} from "@/apiClient";

interface State {
  status: Statistics | null;
  pcaps: PcapInfo[] | null;
  tags: TagInfo[] | null;
  converters: ConverterStatistics[] | null;
  clientConfig: ClientConfig | null;
  pcapOverIPEndpoints: PcapOverIPEndpoint[] | null;
}

export const useRootStore = defineStore("root", {
  state: (): State => {
    setupWebsocket();
    return {
      status: null,
      pcaps: null,
      tags: null,
      converters: null,
      pcapOverIPEndpoints: null,
      clientConfig: null,
    };
  },
  getters: {
    groupedTags: (state) => {
      const res: { [key: string]: TagInfo[] } = {
        tag: [],
        service: [],
        mark: [],
        generated: [],
      };
      if (state.tags != null) {
        for (const tag of state.tags) {
          const type = tag.Name.split("/", 1)[0];
          if (type in res) res[type].push(tag);
          else console.log(`Tag ${tag.Name} has unsupported type`);
        }
      }
      return res;
    },
  },
  actions: {
    updateMark(name: string, streams: number[] | undefined, value: boolean) {
      const streamStore = useStreamStore();
      if (
        streamStore.stream != null &&
        (streams == undefined || streams.includes(streamStore.stream.Stream.ID))
      ) {
        const s = streamStore.stream;
        const current = s.Tags.includes(name);
        if (value && !current) {
          s.Tags.push(name);
        } else if (current && !value) {
          s.Tags = s.Tags.filter((t) => t != name);
        }
      }
      const streamsStore = useStreamsStore();
      if (streamsStore.result != null) {
        for (const s of streamsStore.result.Results) {
          if (streams != undefined && !streams.includes(s.Stream.ID)) continue;
          const current = s.Tags.includes(name);
          if (value && !current) {
            s.Tags.push(name);
          } else if (current && !value) {
            s.Tags = s.Tags.filter((t) => t != name);
          }
        }
      }
    },
    async updateStatus() {
      return APIClient.getStatus()
        .then((data) => (this.status = data))
        .catch(handleAxiosDefaultError);
    },
    async updateTags() {
      return APIClient.getTags()
        .then((data) => (this.tags = data))
        .catch(handleAxiosDefaultError);
    },
    async updatePcapOverIPEndpoints() {
      return APIClient.getPcapOverIPEndpoints()
        .then((data) => (this.pcapOverIPEndpoints = data))
        .catch(handleAxiosDefaultError);
    },
    async addPcapOverIPEndpoint(address: string) {
      return APIClient.addPcapOverIPEndpoint(address)
        .then(() => this.updatePcapOverIPEndpoints())
        .catch(handleAxiosDefaultError);
    },
    async delPcapOverIPEndpoint(address: string) {
      return APIClient.delPcapOverIPEndpoint(address)
        .then(() => {
          this.updatePcapOverIPEndpoints().catch((err) => {
            throw err;
          });
        })
        .catch(handleAxiosDefaultError);
    },
    async updateConverters() {
      return APIClient.getConverters()
        .then((data) => (this.converters = data))
        .catch(handleAxiosDefaultError);
    },
    async getClientConfig() {
      return APIClient.getClientConfig()
        .then((data) => (this.clientConfig = data))
        .catch(handleAxiosDefaultError);
    },
    async addClientConfig(clientConfig: ClientConfig) {
      return APIClient.postClientConfig(clientConfig)
        .then((res) => (this.clientConfig = res))
        .catch(handleAxiosDefaultError);
    },
    async updatePcaps() {
      return APIClient.getPcaps()
        .then((data) => (this.pcaps = data))
        .catch(handleAxiosDefaultError);
    },
    async addTag(name: string, query: string, color: string) {
      return APIClient.addTag(name, query, color)
        .then(() => this.updateTags()) // TODO: not required with websocket?
        .catch(handleAxiosDefaultError);
    },
    async delTag(name: string) {
      return APIClient.delTag(name)
        .then(() => {
          this.updateMark(name, undefined, false);
          this.updateTags().catch((err) => {
            throw err;
          }); // TODO: not required with websocket?
        })
        .catch(handleAxiosDefaultError);
    },
    async changeTagColor(name: string, color: string) {
      return APIClient.changeTagColor(name, color)
        .then(() => this.updateTags()) // TODO: not required with websocket?
        .catch(handleAxiosDefaultError);
    },
    async changeTagDefinition(name: string, definition: string) {
      return APIClient.changeTagDefinition(name, definition)
        .then(() => this.updateTags()) // TODO: not required with websocket?
        .catch(handleAxiosDefaultError);
    },
    async changeTagName(name: string, newName: string) {
      return APIClient.changeTagName(name, newName)
        .then(() => this.updateTags()) // TODO: not required with websocket?
        .catch(handleAxiosDefaultError);
    },
    async setTagConverters(name: string, converters: string[]) {
      return APIClient.converterTagSet(name, converters)
        .then(() => this.updateTags()) // TODO: not required with websocket?
        .catch(handleAxiosDefaultError);
    },
    async resetConverter(name: string) {
      return APIClient.resetConverter(name)
        .then(() => this.updateConverters()) // TODO: not required with websocket?
        .catch(handleAxiosDefaultError);
    },
    async markTagNew(name: string, streams: number[], color: string) {
      return APIClient.markTagNew(name, streams, color)
        .then(() => {
          this.updateMark(name, streams, true);
          this.updateTags().catch((err) => {
            throw err;
          }); // TODO: not required with websocket?
        })
        .catch(handleAxiosDefaultError);
    },
    async markTagAdd(name: string, streams: number[]) {
      return APIClient.markTagAdd(name, streams)
        .then(() => {
          this.updateMark(name, streams, true);
          this.updateTags().catch((err) => {
            throw err;
          }); // TODO: not required with websocket?
        })
        .catch(handleAxiosDefaultError);
    },
    async markTagDel(name: string, streams: number[]) {
      return APIClient.markTagDel(name, streams)
        .then(() => {
          this.updateMark(name, streams, false);
          this.updateTags().catch((err) => {
            throw err;
          }); // TODO: not required with websocket?
        })
        .catch(handleAxiosDefaultError);
    },
  },
});

export function handleAxiosDefaultError(err: unknown) {
  if (axios.isAxiosError<string, unknown>(err))
    throw err.response !== undefined && err.response.data !== ""
      ? err.response.data
      : err.message;
  else throw err;
}
