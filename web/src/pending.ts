// Photos the server does not have yet, kept in IndexedDB so they survive a
// reload: mobile browsers often discard a background tab while the camera is
// open, and the server may be down while scanning. Every call is best effort:
// when storage is unavailable (private mode, blocked site data) capture still
// works, just without the safety net.

/** A lone front photo waiting for its back, or a pair waiting for upload. */
export type Pending = { id: 'front'; front: Blob } | { id: number; front: Blob; back: Blob };

const DB = 'moviecatalog-capture';
const STORE = 'pending';

let opened: Promise<IDBDatabase> | null = null;

function open(): Promise<IDBDatabase> {
  opened ??= new Promise((resolve, reject) => {
    const req = indexedDB.open(DB, 1);
    req.onupgradeneeded = () => req.result.createObjectStore(STORE, { keyPath: 'id' });
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
  opened.catch(() => (opened = null));
  return opened;
}

async function run<T>(mode: IDBTransactionMode, op: (store: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  const db = await open();
  return new Promise((resolve, reject) => {
    const req = op(db.transaction(STORE, mode).objectStore(STORE));
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

function warn(action: string, e: unknown) {
  console.warn(`Could not ${action} unsaved photos in browser storage`, e);
}

export async function loadPending(): Promise<Pending[]> {
  try {
    return await run('readonly', (s) => s.getAll() as IDBRequest<Pending[]>);
  } catch (e) {
    warn('load', e);
    return [];
  }
}

export async function savePending(p: Pending): Promise<void> {
  try {
    await run('readwrite', (s) => s.put(p));
  } catch (e) {
    warn('save', e);
  }
}

export async function removePending(id: Pending['id']): Promise<void> {
  try {
    await run('readwrite', (s) => s.delete(id));
  } catch (e) {
    warn('remove', e);
  }
}
