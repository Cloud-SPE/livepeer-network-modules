// service-registry-daemon client.
//
// Per plan 0013-vtuber §5.1, quote-related code is **dropped** in the
// rewrite — broker URL is the only resolver. This module retains a
// minimal node-roster surface (resolve nodeId → nodeUrl) consumed by
// the relay + worker dispatch. The suite's `quoteRefresher` is gone
// (quote-free flow); `serviceRegistry`'s remaining role is just
// node-URL resolution.

export interface NodeDescriptor {
  nodeId: string;
  nodeUrl: string;
  ethAddress: string;
  capabilities: readonly string[];
}

export interface ServiceRegistryClient {
  listVtuberNodes(): Promise<readonly NodeDescriptor[]>;
  getNode(nodeId: string): Promise<NodeDescriptor | null>;
  close(): Promise<void>;
}
