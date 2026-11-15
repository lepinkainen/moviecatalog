<script lang="ts">
  import { onMount } from 'svelte';
  import WantRow from './WantRow.svelte';
  import { createWant, listWants, searchTMDB, type TMDBResult, type Want, type Wishlist } from './api';

  let wishlist = $state<Wishlist | null>(null);
  let error = $state<string | null>(null);

  let searchType = $state<'movie' | 'tv'>('movie');
  let query = $state('');
  let results = $state<TMDBResult[] | null>(null);
  let searching = $state(false);
  let adding = $state<number | null>(null);

  function message(e: unknown): string {
    return e instanceof Error ? e.message : String(e);
  }

  async function load() {
    try {
      wishlist = await listWants();
      error = null;
    } catch (e) {
      error = message(e);
    }
  }

  onMount(load);

  const wished = $derived(
    // A collected title can be wished for again (an upgrade). A season wish
    // does not stand for the whole series.
    new Set(
      (wishlist?.wants ?? [])
        .filter((w) => !w.collected_on && w.season === null)
        .map((w) => `${w.title.media_type}:${w.title.tmdb_id}`),
    ),
  );

  // Wishes with a deal first, then ones any shop has, then the rest; newest first
  // within each (the server sends newest first and the sort is stable).
  // Collected wishes last, most recently collected first.
  function rank(w: Want): number {
    if (w.collected_on) return 3;
    if (w.offers?.some((o) => o.deal)) return 0;
    if (w.offers?.length) return 1;
    return 2;
  }
  const sections = $derived.by(() => {
    const wants = wishlist?.wants ?? [];
    const collected = wants
      .filter((w) => rank(w) === 3)
      .sort((a, b) => (b.collected_on ?? '').localeCompare(a.collected_on ?? ''));
    return [
      { label: 'Deals', wants: wants.filter((w) => rank(w) === 0) },
      { label: 'In shops', wants: wants.filter((w) => rank(w) === 1) },
      { label: 'Not in shops', wants: wants.filter((w) => rank(w) === 2) },
      { label: 'Collected', wants: collected },
    ].filter((s) => s.wants.length > 0);
  });

  async function search(e: SubmitEvent) {
    e.preventDefault();
    if (!query.trim()) return;
    searching = true;
    error = null;
    try {
      results = (await searchTMDB(query.trim(), searchType)).slice(0, 8);
    } catch (err) {
      error = message(err);
    } finally {
      searching = false;
    }
  }

  async function add(result: TMDBResult) {
    adding = result.tmdb_id;
    error = null;
    try {
      await createWant(result);
      await load();
    } catch (e) {
      error = message(e);
    } finally {
      adding = null;
    }
  }

  // A row's form starts from the stored wish; recreate it when that changes.
  const rowKey = (w: Want) => `${w.id}:${w.formats.join()}:${w.max_price_cents}:${w.notes}:${w.collected_on}`;
</script>

<div class="page">
  <h1 class="visually-hidden">Wishlist</h1>
  <section class="add">
    <h2>Add a wish</h2>
    <search>
      <form onsubmit={search}>
        <select bind:value={searchType} aria-label="Kind">
          <option value="movie">Movie</option>
          <option value="tv">Series</option>
        </select>
        <input bind:value={query} placeholder="Search TMDB" enterkeyhint="search" />
        <button type="submit" disabled={searching || !query.trim()}>{searching ? 'Searching…' : 'Search'}</button>
        {#if results}
          <button type="button" class="link" onclick={() => (results = null)}>Close</button>
        {/if}
      </form>
    </search>

    {#if results}
      {#if results.length === 0}
        <p class="muted">Nothing found.</p>
      {:else}
        <ul class="results">
          {#each results as r (r.tmdb_id)}
            {@const already = wished.has(`${r.media_type}:${r.tmdb_id}`)}
            <li>
              <span class="r-title">
                {r.title}
                {#if r.year}<span class="muted">{r.year}</span>{/if}
                {#if r.original_title && r.original_title !== r.title}
                  <span class="muted">· {r.original_title}</span>
                {/if}
              </span>
              <button type="button" disabled={already || adding !== null} onclick={() => add(r)}>
                {already ? 'On the list' : adding === r.tmdb_id ? 'Adding…' : 'Add'}
              </button>
            </li>
          {/each}
        </ul>
      {/if}
    {/if}
  </section>

  {#if error}<p class="error" role="alert">{error}</p>{/if}

  {#if wishlist}
    {#if !wishlist.offers.configured}
      <p class="notice">Shop prices are off: the server has no connection to the shop watcher (<code>WATCHER_URL</code>).</p>
    {:else if wishlist.offers.error}
      <p class="notice">Shop prices are unavailable right now: {wishlist.offers.error}.</p>
    {/if}

    {#if wishlist.wants.length === 0}
      <p class="muted">The wishlist is empty. Search above to add a movie.</p>
    {/if}

    {#each sections as s (s.label)}
      <section>
        <h2>{s.label} <span class="n">{s.wants.length}</span></h2>
        <ul class="wants">
          {#each s.wants as w (rowKey(w))}
            <WantRow want={w} onchanged={load} />
          {/each}
        </ul>
      </section>
    {/each}
  {:else if !error}
    <p class="muted">Loading…</p>
  {/if}
</div>

<style>
  .page {
    max-width: 60rem;
    margin: 0 auto;
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 1.25rem;
  }

  h2 {
    margin: 0 0 0.6rem;
    font-size: 1rem;
    color: var(--muted);
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }

  .n {
    font-variant-numeric: tabular-nums;
    opacity: 0.7;
  }

  form {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
  }

  input,
  select,
  button {
    font: inherit;
    color: var(--text);
    background: var(--surface);
    border: 1px solid var(--control-border);
    border-radius: 0.5rem;
    padding: 0.5rem 0.75rem;
  }

  input {
    flex: 1 1 14rem;
    min-width: 0;
  }

  button {
    cursor: pointer;
  }

  button:disabled {
    cursor: default;
    opacity: 0.6;
  }

  .link {
    color: var(--link);
    background: none;
    border-color: transparent;
  }

  .results {
    list-style: none;
    margin: 0.5rem 0 0;
    padding: 0;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 0.75rem;
  }

  .results li {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.75rem;
    padding: 0.4rem 0.75rem;
  }

  .results li + li {
    border-top: 1px solid var(--border);
  }

  .results button {
    padding: 0.3rem 0.7rem;
  }

  .r-title {
    min-width: 0;
  }

  .wants {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  .muted {
    color: var(--muted);
  }

  .error {
    color: var(--danger);
    margin: 0;
  }

  .notice {
    margin: 0;
    padding: 0.6rem 0.9rem;
    border: 1px solid var(--border);
    border-radius: 0.5rem;
    background: var(--surface);
    color: var(--muted);
  }

  p {
    margin: 0;
  }
</style>
