<script lang="ts">
  import { untrack } from 'svelte';
  import {
    FORMATS,
    deleteItem,
    duplicateIds,
    getItem,
    identifyItem,
    listItems,
    posterURL,
    searchTMDB,
    thumb,
    titleEdit,
    titleLine,
    updateItem,
    type Item,
    type TMDBResult,
    type TitleEdit,
  } from './api';
  import SeasonPicker from './SeasonPicker.svelte';

  let { item, onchanged }: { item: Item; onchanged: (item: Item) => void } = $props();

  // The form starts from the item as stored; nothing is saved until "Save".
  // The parent recreates this component when the stored item changes, so the
  // initial values are deliberately read only once.
  const initial = untrack(() => item);
  let format = $state(initial.format ?? '');
  let notes = $state(initial.notes);
  let titles = $state<TitleEdit[]>(initial.titles.map(titleEdit));

  let searchType = $state<'movie' | 'tv'>(initial.titles[0]?.media_type ?? 'movie');
  let query = $state(initial.titles[0]?.title ?? '');
  let results = $state<TMDBResult[] | null>(null);
  let searching = $state(false);

  let busy = $state(false);
  let error = $state<string | null>(null);
  let duplicates = $state<Item[]>([]);

  const dirty = $derived(
    format !== (item.format ?? '') ||
      notes !== item.notes ||
      JSON.stringify(titles.map(editKey)) !== JSON.stringify(item.titles.map(editKey)),
  );

  function key(t: { tmdb_id: number | null; media_type: string; title: string }): string {
    return t.tmdb_id ? `${t.media_type}:${t.tmdb_id}` : `text:${t.title}`;
  }

  // A title with its seasons, to tell whether the form has changed.
  function editKey(t: { tmdb_id: number | null; media_type: string; title: string; seasons?: number[] }): string {
    return `${key(t)}|${t.media_type === 'tv' ? [...(t.seasons ?? [])].sort((a, b) => a - b).join(',') : ''}`;
  }

  function message(e: unknown): string {
    return e instanceof Error ? e.message : String(e);
  }

  $effect(() => {
    // Load the items this one is suspected to duplicate, for the side-by-side view.
    const ids = item.status === 'needs_review' ? duplicateIds(item) : [];
    let stale = false;
    Promise.all(ids.map((id) => getItem(id).catch(() => null))).then((found) => {
      if (!stale) duplicates = found.filter((i): i is Item => i !== null);
    });
    return () => {
      stale = true;
    };
  });

  async function run(action: () => Promise<void>) {
    busy = true;
    error = null;
    try {
      await action();
    } catch (e) {
      error = message(e);
    } finally {
      busy = false;
    }
  }

  async function search(event: Event) {
    event.preventDefault();
    if (!query.trim()) return;
    searching = true;
    error = null;
    try {
      // TMDB returns its best matches first; a short list keeps the Save button in reach.
      results = (await searchTMDB(query.trim(), searchType)).slice(0, 8);
    } catch (e) {
      error = message(e);
    } finally {
      searching = false;
    }
  }

  function pick(result: TMDBResult, mode: 'replace' | 'add') {
    const title = titleEdit(result);
    if (mode === 'replace') titles = [title];
    else if (!titles.some((t) => key(t) === key(title))) titles.push(title);
  }

  function addTyped() {
    const title = query.trim();
    if (!title) return;
    const typed: TitleEdit = {
      tmdb_id: null,
      media_type: searchType,
      title,
      original_title: null,
      year: null,
      poster_path: null,
      seasons: [],
    };
    if (!titles.some((t) => key(t) === key(typed))) titles.push(typed);
  }

  // After a decision, move on to the next item waiting for review, or back to the list.
  async function next() {
    const waiting = (await listItems()).filter((i) => i.status === 'needs_review' && i.id !== item.id);
    location.hash = waiting.length > 0 ? `#/items/${waiting[waiting.length - 1].id}` : '#/items';
  }

  const save = () =>
    run(async () => {
      const wasReview = item.status === 'needs_review';
      const saved = await updateItem(item.id, { format: format || undefined, notes, titles });
      if (wasReview) await next();
      else onchanged(saved);
    });

  const approve = () =>
    run(async () => {
      await updateItem(item.id, {});
      await next();
    });

  const rerun = () => run(async () => onchanged(await identifyItem(item.id)));

  const remove = () =>
    run(async () => {
      if (!confirm(`Delete item #${item.id} and its photos? This cannot be undone.`)) return;
      const wasReview = item.status === 'needs_review';
      await deleteItem(item.id);
      if (wasReview) await next();
      else location.hash = '#/items';
    });

  const poster = (path: string | null) => posterURL(path, 92);
</script>

{#if item.status === 'needs_review'}
  <section class="review">
    <h2>Needs review</h2>
    <p>{item.review_reason ?? 'No reason recorded.'}</p>

    {#if duplicates.length > 0}
      <div class="compare">
        {#each [item, ...duplicates] as it (it.id)}
          <a class="side" href="#/items/{it.id}" aria-current={it.id === item.id ? 'true' : undefined}>
            <span class="label">{it.id === item.id ? 'This item' : 'Already in catalogue'} · #{it.id}</span>
            <span class="pics">
              {#each it.images as image (image.id)}
                <img src={thumb(image, 480)} alt="{image.kind} cover of item #{it.id}" />
              {/each}
            </span>
            <span>{titleLine(it)}{it.format ? ` · ${it.format}` : ''}</span>
          </a>
        {/each}
      </div>
      <div class="actions">
        <button
          class="primary"
          disabled={busy || dirty}
          onclick={approve}
          aria-describedby={dirty ? 'approve-hint' : undefined}>Keep both</button
        >
        {#if dirty}<span class="hint" id="approve-hint">Save or undo your corrections first</span>{/if}
        <button class="danger" disabled={busy} onclick={remove}>Delete this one</button>
      </div>
    {:else}
      <div class="actions">
        <button
          class="primary"
          disabled={busy || dirty}
          onclick={approve}
          aria-describedby={dirty ? 'approve-hint' : undefined}>Looks right, approve</button
        >
        {#if dirty}<span class="hint" id="approve-hint">Save or undo your corrections first</span>{/if}
      </div>
    {/if}
  </section>
{/if}

<section>
  <h2>Correct this item</h2>

  <div class="field">
    <span class="name" id="format-label">Format</span>
    <div class="choices" role="group" aria-labelledby="format-label">
      {#each FORMATS as f (f)}
        <button aria-pressed={format === f} onclick={() => (format = f)}>{f}</button>
      {/each}
    </div>
  </div>

  <div class="field">
    <span class="name" id="titles-label">Titles</span>
    <ul class="titles" aria-labelledby="titles-label">
      {#each titles as t, i (key(t))}
        <li>
          <span>
            {t.title}{#if t.year}&nbsp;({t.year}){/if}
            <span class="muted">· {t.media_type === 'tv' ? 'series' : 'movie'}{t.tmdb_id ? '' : ' · typed by hand'}</span>
          </span>
          <button onclick={() => titles.splice(i, 1)} aria-label="Remove {t.title}">Remove</button>
          {#if t.media_type === 'tv'}
            <div class="seasons">
              <SeasonPicker tmdbId={t.tmdb_id} bind:seasons={() => t.seasons ?? [], (v) => (t.seasons = v)} />
            </div>
          {/if}
        </li>
      {:else}
        <li class="muted">No titles. Search below to add one.</li>
      {/each}
    </ul>
  </div>

  <search>
    <form class="search" onsubmit={search}>
      <select bind:value={searchType} aria-label="Search for">
        <option value="movie">Movie</option>
        <option value="tv">Series</option>
      </select>
      <input type="search" placeholder="Search TMDB by title" aria-label="Title to search for" bind:value={query} />
      <button type="submit" disabled={searching || !query.trim()}>{searching ? 'Searching…' : 'Search'}</button>
      <!-- Independent of the search, so a title can be entered when TMDB is down or not configured. -->
      <button type="button" disabled={!query.trim()} onclick={addTyped} title="Add the title exactly as typed, without TMDB">
        Add as typed
      </button>
    </form>
  </search>

  {#if results}
    <ul class="results">
      {#each results as r (r.tmdb_id)}
        <li>
          {#if poster(r.poster_path)}
            <img src={poster(r.poster_path)} alt="" loading="lazy" />
          {:else}
            <span class="noposter"></span>
          {/if}
          <span class="what">
            <strong>{r.title}{#if r.year}&nbsp;({r.year}){/if}</strong>
            {#if r.original_title && r.original_title !== r.title}<span class="muted">{r.original_title}</span>{/if}
            <span class="muted overview">{r.overview}</span>
          </span>
          <span class="pick">
            <button onclick={() => pick(r, 'replace')}>Use this</button>
            <button onclick={() => pick(r, 'add')} title="Add to the titles, for a box set">Add</button>
          </span>
        </li>
      {:else}
        <li class="muted">Nothing found on TMDB. Use “Add as typed” to keep the title as written.</li>
      {/each}
    </ul>
  {/if}

  <label class="field">
    <span class="name">Notes</span>
    <input type="text" bind:value={notes} placeholder="For example: Steelbook, region 1" />
  </label>

  {#if error}<p class="error" role="alert">{error}</p>{/if}

  <div class="actions">
    <button class="primary" disabled={busy || !dirty} onclick={save}>Save</button>
    <button disabled={busy} onclick={rerun} title="Run the automatic identification again from the photos">
      Identify again
    </button>
    <button class="danger" disabled={busy} onclick={remove}>Delete item</button>
  </div>
</section>

<style>
  section {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    padding: 1rem;
    background: var(--surface);
    border-radius: 0.75rem;
  }

  section.review {
    outline: 2px solid var(--danger);
  }

  h2 {
    margin: 0;
    font-size: 1.1rem;
  }

  p {
    margin: 0;
  }

  .muted {
    color: var(--muted);
  }

  .error {
    color: var(--danger);
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
  }

  .name {
    color: var(--muted);
    font-size: 0.9rem;
  }

  button,
  input,
  select {
    font: inherit;
    color: var(--text);
    background: var(--bg);
    border: 1px solid var(--control-border);
    border-radius: 0.5rem;
    padding: 0.5rem 0.8rem;
  }

  button {
    cursor: pointer;
  }

  button:disabled {
    opacity: 0.5;
    cursor: default;
  }

  button.primary,
  button[aria-pressed='true'] {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--on-accent);
  }

  button.danger {
    color: var(--danger);
  }

  .choices,
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
  }

  .actions .danger {
    margin-left: auto;
  }

  .hint {
    align-self: center;
    color: var(--muted);
    font-size: 0.9rem;
  }

  ul {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
  }

  .titles li {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 0.4rem 0.75rem;
    overflow-wrap: anywhere;
  }

  .titles .seasons {
    flex-basis: 100%;
  }

  .search {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
  }

  .search input {
    flex: 1 1 12rem;
    min-width: 0;
  }

  .results li {
    display: flex;
    align-items: flex-start;
    gap: 0.75rem;
    padding: 0.5rem;
    background: var(--bg);
    border-radius: 0.5rem;
  }

  .results img,
  .noposter {
    width: 2.75rem;
    height: 4.1rem;
    object-fit: cover;
    border-radius: 0.25rem;
    background: var(--surface);
    flex: none;
  }

  .what {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    overflow-wrap: anywhere;
  }

  .overview {
    font-size: 0.85rem;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }

  .pick {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    flex: none;
  }

  .pick button {
    background: var(--surface);
  }

  .compare {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(12rem, 1fr));
    gap: 0.75rem;
  }

  .side {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
    padding: 0.6rem;
    border: 1px solid var(--border);
    border-radius: 0.5rem;
    color: inherit;
    text-decoration: none;
    overflow-wrap: anywhere;
  }

  .side[aria-current='true'] {
    border-color: var(--danger);
  }

  .label {
    font-size: 0.85rem;
    color: var(--muted);
  }

  .pics {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 0.4rem;
  }

  .pics img {
    width: 100%;
    aspect-ratio: 3 / 4;
    object-fit: cover;
    border-radius: 0.375rem;
    background: var(--bg);
  }
</style>
