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

export interface Run {
  id: number;
  step: string;
  model: string | null;
  request: string | null;
  response: string | null;
  error: string | null;
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

export async function createItem(front: File, back: File): Promise<Item> {
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

export async function deleteItem(id: number): Promise<void> {
  await check(await fetch(`/api/items/${id}`, { method: 'DELETE' }));
}
