/** The library's place in the hash: #/library, #/library/{title id}, either with ?q=…. */
export interface LibraryRoute {
  selected: number | null;
  q: string | null;
}

export function parseLibraryHash(hash: string): LibraryRoute | null {
  const m = hash.match(/^#\/library(?:\/(\d+))?(?:\?(.*))?$/);
  if (!m) return null;
  return { selected: m[1] ? Number(m[1]) : null, q: new URLSearchParams(m[2] ?? '').get('q') };
}
