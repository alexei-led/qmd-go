// Thin OpenClaw plugin wrapper that proxies memory_search to qmd-go HTTP sidecar.

const DEFAULT_PORT = 18790;

interface SearchResult {
  path: string;
  title: string;
  snippet: string;
  score: number;
  lineStart?: number;
  lineEnd?: number;
}

export default (api: any) => {
  const port = process.env.QMD_PORT || DEFAULT_PORT;

  api.registerTool("memory_search", {
    description: "Semantic search over memory files via QMD",
    inputSchema: {
      type: "object",
      properties: {
        query: { type: "string", description: "Search query text" },
        limit: { type: "number", description: "Maximum results" },
      },
      required: ["query"],
    },
    handler: async ({
      query,
      limit,
    }: {
      query: string;
      limit?: number;
    }): Promise<SearchResult[]> => {
      const resp = await fetch(`http://localhost:${port}/search`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          searches: [{ type: "vec", query }],
          limit: limit || 8,
          collections: ["openclaw-memory"],
        }),
      });
      if (!resp.ok) {
        throw new Error(`QMD sidecar error: ${resp.status} ${resp.statusText}`);
      }
      return resp.json();
    },
  });
};
