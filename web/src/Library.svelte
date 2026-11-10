<script lang="ts">
  import { onMount } from 'svelte';
  import {
    FORMATS,
    formatRank,
    listItems,
    posterURL,
    statusLabel,
    thumb,
    titleLine,
    type Item,
    type Title,
  } from './api';

  /**
   * selected: the title id from the URL whose discs are shown in the side panel.
   * q: a search from the URL (#/library?q=…), so a browser keyword search can open it.
   */
  let { selected, q }: { selected: number | null; q: string | null } = $props();

  // One card per movie or series, however many discs it is on.
  interface Entry {
    key: string;
    /** Every title id in the group; hand-typed titles get a new id per item. */
    ids: number[];
    title: Title;
    sortTitle: string;
    best: string | null;
    items: Item[];
    /** None of its discs has been confirmed yet, so the match is still a guess. */
    unconfirmed: boolean;
    addedAt: string;
  }

  const FORMAT_FILTERS = ['all', ...[...FORMATS].reverse()] as const;
  type FormatFilter = (typeof FORMAT_FILTERS)[number];
  const KINDS = [
    { key: 'all', label: 'All' },
    { key: 'movie', label: 'Movies' },
    { key: 'tv', label: 'Series' },
  ] as const;
  type Kind = (typeof KINDS)[number]['key'];
  type Sort = 'title' | 'year' | 'added';

  const REFRESH_MS = 4000;

  let items = $state<Item[] | null>(null);
  let error = $state<string | null>(null);
  let query = $state('');
  let format = $state<FormatFilter>('all');
  let kind = $state<Kind>('all');
  let sort = $state<Sort>('title');
  let searchInput = $state<HTMLInputElement>();

  function groupKey(t: Title): string {
    if (t.tmdb_id) return `${t.media_type}:${t.tmdb_id}`;
    return `text:${t.media_type}:${t.title.trim().toLowerCase()}:${t.year ?? ''}`;
  }

  // Words a shop listing adds around the title ("Alien (1979) [4K UHD + Blu-ray] Steelbook").
  const LISTING_WORDS = new Set([
    'blu', 'ray', 'bluray', 'dvd', 'uhd', '4k', 'hd', 'ultra', '2d', '3d', 'steelbook', 'edition',
    'collectors', 'limited', 'special', 'import', 'region', 'free', 'disc', 'discs', 'boxset', 'box', 'set',
  ]);

  // Lower case, no accents, punctuation as spaces: "Amélie!" → "amelie".
  function normalize(s: string): string {
    return s
      .normalize('NFD')
      .replace(/\p{M}/gu, '')
      .toLowerCase()
      .replace(/[^\p{L}\p{N}]+/gu, ' ')
      .trim();
  }

  function searchTerms(raw: string): string[] {
    const words = normalize(raw).split(' ').filter(Boolean);
    const kept = words.filter((w) => !LISTING_WORDS.has(w));
    // A search made only of such words ("Blu-ray") is still a search for them.
    return kept.length > 0 ? kept : words;
  }

  // Every term must appear in the title or original title. A year-like term
  // also matches the release year give or take one, since listings disagree.
  function matches(t: Title, terms: string[]): boolean {
    const text = ` ${normalize(t.title)} ${normalize(t.original_title ?? '')} `;
    // "spiderman" should find "Spider-Man".
    const compact = text.replaceAll(' ', '');
    return terms.every((term) => {
      if (text.includes(` ${term}`) || compact.includes(term)) return true;
      const year = /^(19|20)\d\d$/.test(term) ? Number(term) : null;
      return year !== null && t.year !== null && Math.abs(t.year - year) <= 1;
    });
  }

  // "The Thing" files under T, as on a shelf.
  function sortTitle(title: string): string {
    return title.replace(/^(the|a|an)\s+/i, '');
  }

  const entries = $derived.by(() => {
    const byKey = new Map<string, Entry>();
    for (const item of items ?? []) {
      if (item.status === 'captured') continue;
      for (const t of item.titles) {
        const k = groupKey(t);
        let e = byKey.get(k);
        if (!e) {
          e = {
            key: k,
            ids: [],
            title: t,
            sortTitle: sortTitle(t.title),
            best: null,
            items: [],
            unconfirmed: true,
            addedAt: item.captured_at,
          };
          byKey.set(k, e);
        }
        if (!e.ids.includes(t.id)) e.ids.push(t.id);
        if (e.items.includes(item)) continue;
        e.items.push(item);
        if (formatRank(item.format) > formatRank(e.best)) e.best = item.format;
        if (item.status === 'identified') e.unconfirmed = false;
        if (item.captured_at > e.addedAt) e.addedAt = item.captured_at;
      }
    }
    for (const e of byKey.values()) e.items.sort((a, b) => formatRank(b.format) - formatRank(a.format));
    return [...byKey.values()];
  });

  const notInLibrary = $derived((items ?? []).filter((i) => i.status === 'captured' || i.titles.length === 0).length);

  const kindMatches = $derived(entries.filter((e) => kind === 'all' || e.title.media_type === kind));

  const formatCounts = $derived.by(() => {
    const c: Record<string, number> = { all: kindMatches.length };
    for (const e of kindMatches) if (e.best) c[e.best] = (c[e.best] ?? 0) + 1;
    return c;
  });

  const visible = $derived.by(() => {
    const list = kindMatches.filter((e) => {
      if (format !== 'all' && e.best !== format) return false;
      return terms.length === 0 || matches(e.title, terms);
    });
    const byTitle = (a: Entry, b: Entry) => a.sortTitle.localeCompare(b.sortTitle, undefined, { sensitivity: 'base' });
    switch (sort) {
      case 'title':
        return list.sort(byTitle);
      case 'year':
        return list.sort((a, b) => (b.title.year ?? 0) - (a.title.year ?? 0) || byTitle(a, b));
      case 'added':
        return list.sort((a, b) => b.addedAt.localeCompare(a.addedAt) || byTitle(a, b));
    }
  });

  const terms = $derived(searchTerms(query));

  // A search that comes in through the URL replaces the current one. Typing
  // keeps the URL in step without adding history entries.
  $effect(() => {
    if (q !== null) query = q;
  });

  function onsearch() {
    const hash = query.trim() ? `#/library?q=${encodeURIComponent(query.trim())}` : '#/library';
    history.replaceState(history.state, '', hash);
  }

  const current = $derived(selected === null ? null : (entries.find((e) => e.ids.includes(selected)) ?? null));

  async function load() {
    try {
      items = await listItems();
      error = null;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  onMount(() => {
    load();
    // The common visit is "do I own this?", so be ready to type. Skipped on
    // touch screens, where it would pop up the keyboard.
    if (matchMedia('(pointer: fine)').matches) searchInput?.focus();
    const timer = setInterval(() => {
      if (error || items?.some((i) => i.status === 'captured')) load();
    }, REFRESH_MS);
    return () => clearInterval(timer);
  });

  function close() {
    location.hash = '#/library';
  }

  function onkeydown(event: KeyboardEvent) {
    const target = event.target as HTMLElement;
    const typing = target.matches('input, textarea, select');
    if (event.key === '/' && !typing) {
      event.preventDefault();
      searchInput?.focus();
    } else if (event.key === 'Escape') {
      if (typing) target.blur();
      else if (selected !== null) close();
    }
  }

  function tmdbURL(t: Title): string | null {
    return t.tmdb_id ? `https://www.themoviedb.org/${t.media_type}/${t.tmdb_id}` : null;
  }
</script>

<svelte:window {onkeydown} />

<div class="layout" class:open={selected !== null}>
  <main>
    <div class="controls">
      <input
        type="search"
        placeholder="Do I own…?  Type or paste a title  ( / )"
        aria-label="Search titles"
        bind:value={query}
        oninput={onsearch}
        bind:this={searchInput}
      />
      <div class="group" role="group" aria-label="Best format">
        {#each FORMAT_FILTERS as f (f)}
          <button aria-pressed={format === f} onclick={() => (format = f)}>
            {f === 'all' ? 'All' : f} <span class="n">{formatCounts[f] ?? 0}</span>
          </button>
        {/each}
      </div>
      <div class="group" role="group" aria-label="Kind">
        {#each KINDS as k (k.key)}
          <button aria-pressed={kind === k.key} onclick={() => (kind = k.key)}>{k.label}</button>
        {/each}
      </div>
      <label class="sort">
        Sort
        <select bind:value={sort}>
          <option value="title">Title</option>
          <option value="year">Year</option>
          <option value="added">Recently added</option>
        </select>
      </label>
    </div>

    {#if error}
      <p class="error">Could not load the library: {error}</p>
    {/if}

    {#if items === null}
      {#if !error}<p class="empty">Loading…</p>{/if}
    {:else if entries.length === 0}
      <p class="empty">No identified titles yet. <a href="#/items">See the catalogue.</a></p>
    {:else if visible.length === 0}
      {#if terms.length > 0}
        <div class="verdict">
          <strong>Not in your collection</strong>
          <span class="muted">
            Nothing matches “{query.trim()}”{format !== 'all' || kind !== 'all' ? ' with the current filters' : ''}.
          </span>
        </div>
      {:else}
        <p class="empty">No titles match.</p>
      {/if}
    {:else}
      <ul class="grid">
        {#each visible as entry (entry.key)}
          {@const poster = posterURL(entry.title.poster_path, 342)}
          <li>
            <a
              href="#/library/{entry.ids[0]}"
              class="card"
              class:selected={current === entry}
              aria-current={current === entry ? 'true' : undefined}
            >
              <span class="poster">
                {#if poster}
                  <img src={poster} alt="" loading="lazy" />
                {:else}
                  <span class="noposter">{entry.title.title}</span>
                {/if}
                {#if entry.best}
                  <span class="badge f-{formatRank(entry.best)}">{entry.best}</span>
                {/if}
                {#if entry.items.length > 1}
                  <span class="copies" title="{entry.items.length} discs">×{entry.items.length}</span>
                {/if}
              </span>
              <span class="name">{entry.title.title}</span>
              <span class="meta">
                {entry.title.year ?? '—'}
                {#if entry.title.media_type === 'tv'}· series{/if}
                {#if entry.unconfirmed}<span class="review">· in review</span>{/if}
              </span>
            </a>
          </li>
        {/each}
      </ul>
    {/if}

    {#if notInLibrary > 0}
      <p class="empty small">
        {notInLibrary} disc{notInLibrary === 1 ? ' is' : 's are'} not shown because {notInLibrary === 1
          ? 'it is'
          : 'they are'} still waiting or unidentified. <a href="#/items">Open the catalogue.</a>
      </p>
    {/if}
  </main>

  {#if selected !== null}
    <button class="backdrop" aria-label="Close" onclick={close}></button>
    <aside aria-label="Title details">
      <button class="close" aria-label="Close" onclick={close}>✕</button>
      {#if current}
        {@const t = current.title}
        {@const poster = posterURL(t.poster_path, 342)}
        {@const tmdb = tmdbURL(t)}
        {#if poster}<img class="big" src={poster} alt="{t.title} poster" />{/if}
        <h2>{t.title}{#if t.year}&nbsp;<span class="muted">({t.year})</span>{/if}</h2>
        {#if t.original_title && t.original_title !== t.title}
          <p class="muted">{t.original_title}</p>
        {/if}
        <p class="muted">
          {t.media_type === 'tv' ? 'Series' : 'Movie'}
          {#if tmdb}· <a href={tmdb} target="_blank" rel="noopener">TMDB</a>{:else}· no TMDB match{/if}
        </p>
        <p>
          Best owned: <strong>{current.best ?? 'unknown format'}</strong>
          {#if current.unconfirmed}<span class="review">· still in review</span>{/if}
        </p>

        <h3>On {current.items.length} disc{current.items.length === 1 ? '' : 's'}</h3>
        <ul class="discs">
          {#each current.items as item (item.id)}
            {@const front = item.images.find((i) => i.kind === 'front')}
            <li>
              <a href="#/items/{item.id}">
                {#if front}<img src={thumb(front, 160)} alt="" loading="lazy" />{:else}<span class="noimg"></span>{/if}
                <span class="text">
                  <span><strong>{item.format ?? 'Unknown format'}</strong> <span class="muted">#{item.id}</span></span>
                  {#if item.titles.length > 1}<span class="muted">{titleLine(item)}</span>{/if}
                  {#if item.notes}<span class="muted">{item.notes}</span>{/if}
                  {#if item.status !== 'identified'}<span class="review">{statusLabel(item.status)}</span>{/if}
                </span>
              </a>
            </li>
          {/each}
        </ul>
      {:else if items !== null}
        <p class="muted">This title is no longer in the library.</p>
      {/if}
    </aside>
  {/if}
</div>

<style>
  .layout {
    max-width: 100rem;
    margin: 0 auto;
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: 1rem;
    padding: 1rem;
  }

  main {
    display: flex;
    flex-direction: column;
    gap: 1rem;
    min-width: 0;
  }

  .controls {
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem;
    align-items: center;
  }

  input,
  select {
    font: inherit;
    color: var(--text);
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 0.5rem;
    padding: 0.5rem 0.75rem;
  }

  input {
    flex: 1 1 16rem;
  }

  .group {
    display: flex;
    gap: 0.25rem;
  }

  .group button {
    font: inherit;
    color: var(--muted);
    background: none;
    border: 1px solid transparent;
    border-radius: 0.5rem;
    padding: 0.45rem 0.7rem;
    cursor: pointer;
  }

  .group button[aria-pressed='true'] {
    color: var(--text);
    background: var(--surface);
    border-color: var(--border);
  }

  .n {
    font-variant-numeric: tabular-nums;
    opacity: 0.7;
  }

  .sort {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    color: var(--muted);
  }

  .grid {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(9.5rem, 1fr));
    gap: 1.25rem 1rem;
  }

  .card {
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
    color: inherit;
    text-decoration: none;
    border-radius: 0.6rem;
  }

  .poster {
    position: relative;
    display: block;
    aspect-ratio: 2 / 3;
    border-radius: 0.6rem;
    overflow: hidden;
    background: var(--surface);
    margin-bottom: 0.3rem;
    transition: transform 120ms ease;
  }

  .card:hover .poster,
  .card:focus-visible .poster {
    transform: translateY(-2px);
  }

  .card.selected .poster {
    outline: 3px solid var(--accent);
    outline-offset: 2px;
  }

  .poster img {
    display: block;
    width: 100%;
    height: 100%;
    object-fit: cover;
  }

  .noposter {
    display: flex;
    align-items: center;
    justify-content: center;
    height: 100%;
    padding: 0.75rem;
    text-align: center;
    color: var(--muted);
    font-weight: 600;
    border: 1px dashed var(--border);
    border-radius: 0.6rem;
    overflow-wrap: anywhere;
  }

  .badge,
  .copies {
    position: absolute;
    top: 0.4rem;
    font-size: 0.75rem;
    font-weight: 700;
    padding: 0.1rem 0.4rem;
    border-radius: 0.3rem;
    box-shadow: 0 1px 3px rgb(0 0 0 / 0.35);
  }

  .badge {
    left: 0.4rem;
  }

  .copies {
    right: 0.4rem;
    background: rgb(0 0 0 / 0.7);
    color: #fff;
  }

  /* Ranks from formatRank: 0 DVD, 1 Blu-ray, 2 UHD. */
  .f-0 {
    background: var(--surface);
    color: var(--text);
  }
  .f-1 {
    background: var(--accent);
    color: var(--on-accent);
  }
  .f-2 {
    background: var(--text);
    color: var(--bg);
  }

  .name {
    font-weight: 600;
    line-height: 1.25;
    overflow-wrap: anywhere;
  }

  .meta {
    font-size: 0.85rem;
    color: var(--muted);
  }

  .review,
  .error {
    color: var(--danger);
  }

  .muted {
    color: var(--muted);
    font-weight: 400;
  }

  .empty {
    color: var(--muted);
    text-align: center;
    padding: 2rem 0;
  }

  .verdict {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.25rem;
    padding: 2rem 1rem;
    background: var(--surface);
    border-radius: 0.75rem;
    text-align: center;
  }

  .verdict strong {
    font-size: 1.3rem;
  }

  .empty.small {
    padding: 0.5rem 0;
    font-size: 0.9rem;
  }

  a {
    color: var(--accent);
  }

  aside {
    position: relative;
    background: var(--surface);
    border-radius: 0.75rem;
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  aside h2 {
    margin: 0;
    font-size: 1.3rem;
    padding-right: 2rem;
    overflow-wrap: anywhere;
  }

  aside h3 {
    margin: 0.75rem 0 0;
    font-size: 1rem;
  }

  aside p {
    margin: 0;
  }

  .big {
    width: 60%;
    align-self: center;
    border-radius: 0.5rem;
    margin-bottom: 0.5rem;
  }

  .close {
    position: absolute;
    top: 0.6rem;
    right: 0.6rem;
    font: inherit;
    color: var(--muted);
    background: none;
    border: none;
    padding: 0.3rem 0.5rem;
    cursor: pointer;
    border-radius: 0.4rem;
  }

  .close:hover {
    background: var(--bg);
  }

  .discs {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
  }

  .discs a {
    display: flex;
    gap: 0.75rem;
    align-items: center;
    padding: 0.4rem;
    border-radius: 0.5rem;
    color: inherit;
    text-decoration: none;
    background: var(--bg);
  }

  .discs a:hover {
    outline: 1px solid var(--border);
  }

  .discs img,
  .noimg {
    width: 3rem;
    height: 4rem;
    object-fit: cover;
    border-radius: 0.3rem;
    background: var(--surface);
    flex: none;
  }

  .text {
    display: flex;
    flex-direction: column;
    min-width: 0;
    font-size: 0.9rem;
    overflow-wrap: anywhere;
  }

  .backdrop {
    display: none;
  }

  /* Wide screens: the panel sits beside the grid. */
  @media (min-width: 60rem) {
    .layout.open {
      grid-template-columns: minmax(0, 1fr) 24rem;
    }

    aside {
      position: sticky;
      top: 1rem;
      align-self: start;
      max-height: calc(100vh - 2rem);
      overflow: auto;
    }
  }

  /* Narrow screens: the panel slides over the grid. */
  @media (max-width: 59.99rem) {
    .backdrop {
      display: block;
      position: fixed;
      inset: 0;
      border: none;
      background: rgb(0 0 0 / 0.45);
      cursor: default;
    }

    aside {
      position: fixed;
      top: 0;
      right: 0;
      bottom: 0;
      width: min(26rem, 100%);
      border-radius: 0;
      overflow: auto;
    }
  }
</style>
