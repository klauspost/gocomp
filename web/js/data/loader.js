// The only module in the site that knows where the JSON lives. If you ever
// need fetch() in a view, the layering has been broken.

export function createLoader({ base = './data/' } = {}) {
  const runs = new Map(); // id -> Promise<runFile>; memoising the promise dedupes in-flight loads

  async function get(path, opts) {
    const res = await fetch(base + path, opts);
    if (!res.ok) throw new Error(`${path}: ${res.status} ${res.statusText}`);
    return res.json();
  }

  let indexPromise = null;
  return {
    index() {
      // The manifest changes whenever a run is merged, so it is never cached.
      indexPromise ??= get('index.json', { cache: 'no-cache' });
      return indexPromise;
    },
    run(stub) {
      let p = runs.get(stub.id);
      if (!p) {
        p = get(stub.file);
        runs.set(stub.id, p);
      }
      return p;
    },
    loaded(id) {
      return runs.has(id);
    },
  };
}
