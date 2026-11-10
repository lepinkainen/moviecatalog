export interface ItemImage {
  id: number;
  kind: 'front' | 'back';
  content_type: string;
  url: string;
}

export interface Title {
  id: number;
  tmdb_id: number | null;
  media_type: 'movie' | 'tv';
  title: string;
  original_title: string | null;
  year: number | null;
  poster_path: string | null;
}

export interface Item {
  id: number;
  captured_at: string;
  status: string;
  format: string | null;
  barcode: string | null;
  notes: string;
  confidence: 'high' | 'medium' | 'low' | null;
  review_reason: string | null;
  images: ItemImage[];
  titles: Title[];
}

export const FORMATS = ['DVD', 'Blu-ray', 'UHD'] as const;

/** Higher is better; formats outside FORMATS rank below all of them. */
export function formatRank(format: string | null): number {
  return FORMATS.indexOf(format as (typeof FORMATS)[number]);
}

/** URL of a TMDB poster at one of TMDB's fixed widths. */
export function posterURL(path: string | null, width: 92 | 185 | 342): string | null {
  return path ? `https://image.tmdb.org/t/p/w${width}${path}` : null;
}

/** A title as sent when correcting an item; tmdb_id is null for a hand-typed one. */
export interface TitleEdit {
  tmdb_id: number | null;
  media_type: 'movie' | 'tv';
  title: string;
  original_title: string | null;
  year: number | null;
  poster_path: string | null;
}

/**
 * Copies just the fields of a TitleEdit. The server rejects unknown fields, so
 * a stored Title (which has an id) or a TMDBResult (which has an overview)
 * must not be sent as is.
 */
export function titleEdit(t: TitleEdit): TitleEdit {
  return {
    tmdb_id: t.tmdb_id,
    media_type: t.media_type,
    title: t.title,
    original_title: t.original_title,
    year: t.year,
    poster_path: t.poster_path,
  };
}

/** A manual correction. Fields left out stay as they are. */
export interface ItemEdit {
  format?: string;
  notes?: string;
  titles?: TitleEdit[];
}

export interface TMDBResult extends TitleEdit {
  tmdb_id: number;
  overview: string;
}

export interface Run {
  id: number;
  step: string;
  model: string | null;
  request: string | null;
  response: string | null;
  error: string | null;
  input_tokens: number | null;
  output_tokens: number | null;
  /** Estimated from the list price; null when no price was configured. */
  cost_usd: number | null;
  created_at: string;
}

/** URL of an image scaled down to one of the widths the server offers. */
export function thumb(image: ItemImage, width: 160 | 480 | 1200): string {
  return `${image.url}?w=${width}`;
}

export function statusLabel(status: string): string {
  switch (status) {
    case 'captured':
      return 'Waiting';
    case 'identified':
      return 'Identified';
    case 'needs_review':
      return 'Needs review';
    default:
      return status;
  }
}

/** One-line description of what an item contains. */
export function titleLine(item: Item): string {
  if (item.titles.length === 0) {
    return item.status === 'captured' ? 'Waiting for identification' : 'Not identified';
  }
  return item.titles.map((t) => (t.year ? `${t.title} (${t.year})` : t.title)).join(' / ');
}

async function check(resp: Response): Promise<Response> {
  if (resp.ok) return resp;
  let message = `${resp.status} ${resp.statusText}`;
  try {
    const body = await resp.json();
    if (typeof body?.error === 'string') message = body.error;
  } catch {
    // Not JSON (for example a proxy error page); keep the status line.
  }
  throw new Error(message);
}

export async function createItem(front: Blob, back: Blob): Promise<Item> {
  const form = new FormData();
  form.append('front', front);
  form.append('back', back);
  const resp = await check(await fetch('/api/items', { method: 'POST', body: form }));
  return resp.json();
}

export async function listItems(): Promise<Item[]> {
  const resp = await check(await fetch('/api/items'));
  return resp.json();
}

export async function getItem(id: number): Promise<Item> {
  const resp = await check(await fetch(`/api/items/${id}`));
  return resp.json();
}

export async function listRuns(id: number): Promise<Run[]> {
  const resp = await check(await fetch(`/api/items/${id}/runs`));
  return resp.json();
}

/** Saves a correction and marks the item as identified. An empty edit approves it as is. */
export async function updateItem(id: number, edit: ItemEdit): Promise<Item> {
  const resp = await check(
    await fetch(`/api/items/${id}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ...edit, titles: edit.titles?.map(titleEdit) }),
    }),
  );
  return resp.json();
}

/** Queues the item to be identified again from its photos. */
export async function identifyItem(id: number): Promise<Item> {
  const resp = await check(await fetch(`/api/items/${id}/identify`, { method: 'POST' }));
  return resp.json();
}

export async function searchTMDB(query: string, type: 'movie' | 'tv'): Promise<TMDBResult[]> {
  const params = new URLSearchParams({ q: query, type });
  const resp = await check(await fetch(`/api/tmdb/search?${params}`));
  return resp.json();
}

/** Ids of the other items a review reason points at ("same barcode as item #3"). */
export function duplicateIds(item: Item): number[] {
  const ids = [...(item.review_reason ?? '').matchAll(/item #(\d+)/g)].map((m) => Number(m[1]));
  return [...new Set(ids)].filter((id) => id !== item.id);
}

export async function deleteItem(id: number): Promise<void> {
  await check(await fetch(`/api/items/${id}`, { method: 'DELETE' }));
}
