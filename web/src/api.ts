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

/** Formats a wish can accept: the ones the shop watcher tracks. */
export const WANT_FORMATS = ['Blu-ray', 'UHD'] as const;

/** A shop listing for a wished-for movie, from cdon-watcher. */
export interface Offer {
  store_key: string;
  store: string;
  title: string;
  format: string;
  condition: string;
  price_cents: number;
  /** Set when the listing shows a discount. */
  regular_price_cents: number | null;
  currency: string;
  /** Only in_stock can be a deal. Kept open to values added later. */
  availability: 'in_stock' | 'supplier_order' | 'preorder' | 'out_of_stock' | 'unknown' | (string & {});
  url: string;
  image_url: string | null;
  seen_at: string;
  /** The watcher's price history page for the listing, when known. */
  history_url: string | null;
  /** In stock and within the wish's price cap. */
  deal: boolean;
}

export interface Want {
  id: number;
  title: Title;
  formats: string[];
  max_price_cents: number | null;
  notes: string;
  created_at: string;
  /** Formats of the discs already holding this title. */
  owned_formats: string[];
  /** Null when offers could not be looked up. */
  offers: Offer[] | null;
  /** When the watcher last searched the shops for it; see Wishlist.offers.searches. */
  searched_at: string | null;
}

export interface Wishlist {
  wants: Want[];
  offers: {
    configured: boolean;
    /** Whether searched_at is known; without it an empty result may just mean "not searched yet". */
    searches: boolean;
    error: string | null;
  };
}

export interface WantEdit {
  formats: string[];
  max_price_cents: number | null;
  notes: string;
}

export async function listWants(): Promise<Wishlist> {
  const resp = await check(await fetch('/api/wants'));
  return resp.json();
}

export async function createWant(title: TitleEdit, edit?: WantEdit): Promise<Want> {
  const resp = await check(
    await fetch('/api/wants', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ title: titleEdit(title), ...edit }),
    }),
  );
  return resp.json();
}

export async function updateWant(id: number, edit: WantEdit): Promise<Want> {
  const resp = await check(
    await fetch(`/api/wants/${id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(edit),
    }),
  );
  return resp.json();
}

export async function deleteWant(id: number): Promise<void> {
  await check(await fetch(`/api/wants/${id}`, { method: 'DELETE' }));
}

/** "5 min ago", "3 h ago", "2 days ago". */
export function ago(iso: string, now = Date.now()): string {
  const minutes = Math.max(0, Math.round((now - new Date(iso).getTime()) / 60000));
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 48) return `${hours} h ago`;
  return `${Math.round(hours / 24)} days ago`;
}

/** "12,99 €" style, as the shops show it. */
export function euros(cents: number): string {
  return new Intl.NumberFormat('fi-FI', { style: 'currency', currency: 'EUR' }).format(cents / 100);
}
