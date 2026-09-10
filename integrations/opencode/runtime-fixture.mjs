// In-memory stand-in for the pinned generated transport, never a network client.
export function runtimeFixture(respond) {
  let current;
  return { _client: {
    getConfig: () => ({ fetch: async (request) => respond(current, request) }),
    get: async (options) => {
      current = options;
      const url = new URL(options.url.replace("{id}", encodeURIComponent(options.path.id)), "http://fixture.invalid");
      for (const [key, value] of Object.entries(options.query)) url.searchParams.set(key, String(value));
      const result = await options.fetch(new Request(url, { signal: options.signal, headers: { authorization: "fixture-only" }, redirect: options.redirect }));
      return { data: result.body };
    },
  } };
}
export const jsonResponse = (data) => new Response(JSON.stringify(data), { headers: { "Content-Type": "application/json" } });
