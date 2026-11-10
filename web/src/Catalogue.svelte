<script lang="ts">
  import { onMount } from 'svelte';
  import { listItems, statusLabel, thumb, titleLine, type Item } from './api';

  const FILTERS = [
    { key: 'all', label: 'All' },
    { key: 'identified', label: 'Identified' },
    { key: 'needs_review', label: 'Needs review' },
    { key: 'captured', label: 'Waiting' },
  ] as const;
  type Filter = (typeof FILTERS)[number]['key'];

  // While items are waiting for identification the list refreshes itself.
  const REFRESH_MS = 4000;

  let items = $state<Item[] | null>(null);
  let error = $state<string | null>(null);
  let filter = $state<Filter>('all');
  let query = $state('');

  const counts = $derived.by(() => {
    const c: Record<string, number> = { all: items?.length ?? 0 };
    for (const item of items ?? []) c[item.status] = (c[item.status] ?? 0) + 1;
    return c;
  });

  const visible = $derived.by(() => {
    const needle = query.trim().toLowerCase();
    return (items ?? []).filter((item) => {
      if (filter !== 'all' && item.status !== filter) return false;
      if (!needle) return true;
      return item.titles.some(
        (t) => t.title.toLowerCase().includes(needle) || (t.original_title ?? '').toLowerCase().includes(needle),
      );
    });
  });

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
    const timer = setInterval(() => {
      if (error || items?.some((i) => i.status === 'captured')) load();
    }, REFRESH_MS);
    return () => clearInterval(timer);
  });
</script>

<main>
  <div class="controls">
    <input type="search" placeholder="Filter by title" aria-label="Filter by title" bind:value={query} />
    <div class="filters">
      {#each FILTERS as f (f.key)}
        <button aria-pressed={filter === f.key} onclick={() => (filter = f.key)}>
          {f.label} <span class="n">{counts[f.key] ?? 0}</span>
        </button>
      {/each}
    </div>
  </div>

  {#if error}
    <p class="error">Could not load the catalogue: {error}</p>
  {/if}

  {#if items === null}
    {#if !error}<p class="empty">Loading…</p>{/if}
  {:else if items.length === 0}
    <p class="empty">Nothing captured yet. <a href="#/capture">Capture the first case.</a></p>
  {:else if visible.length === 0}
    <p class="empty">No items match.</p>
  {:else}
    <ul>
      {#each visible as item (item.id)}
        {@const front = item.images.find((i) => i.kind === 'front')}
        <li>
          <a href="#/items/{item.id}">
            {#if front}
              <img src={thumb(front, 160)} alt="" loading="lazy" />
            {:else}
              <span class="noimg"></span>
            {/if}
            <span class="text">
              <span class="title" class:pending={item.titles.length === 0}>{titleLine(item)}</span>
              {#if item.notes}<span class="sub">{item.notes}</span>{/if}
              {#if item.status === 'needs_review' && item.review_reason}
                <span class="sub review">{item.review_reason}</span>
              {/if}
            </span>
            {#if item.format}<span class="format">{item.format}</span>{/if}
            <span class="status {item.status}">{statusLabel(item.status)}</span>
          </a>
        </li>
      {/each}
    </ul>
  {/if}
</main>

<style>
  main {
    max-width: 56rem;
    margin: 0 auto;
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .controls {
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem;
    align-items: center;
  }

  input {
    flex: 1 1 14rem;
    font: inherit;
    color: var(--text);
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 0.5rem;
    padding: 0.55rem 0.75rem;
  }

  .filters {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem;
  }

  button {
    font: inherit;
    color: var(--muted);
    background: none;
    border: 1px solid transparent;
    border-radius: 0.5rem;
    padding: 0.45rem 0.7rem;
    cursor: pointer;
  }

  button[aria-pressed='true'] {
    color: var(--text);
    background: var(--surface);
    border-color: var(--border);
  }

  .n {
    font-variant-numeric: tabular-nums;
    opacity: 0.7;
  }

  ul {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  li a {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.5rem 0.75rem 0.5rem 0.5rem;
    border-radius: 0.75rem;
    background: var(--surface);
    color: inherit;
    text-decoration: none;
  }

  li a:hover {
    outline: 1px solid var(--border);
  }

  img,
  .noimg {
    width: 3rem;
    height: 4rem;
    object-fit: cover;
    border-radius: 0.375rem;
    background: var(--bg);
    flex: none;
  }

  .text {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    overflow-wrap: anywhere;
  }

  .title {
    font-weight: 600;
  }

  .title.pending {
    font-weight: 400;
    color: var(--muted);
  }

  .sub {
    color: var(--muted);
    font-size: 0.9rem;
  }

  .format {
    flex: none;
    font-size: 0.85rem;
    font-weight: 600;
    padding: 0.15rem 0.5rem;
    border: 1px solid var(--border);
    border-radius: 0.375rem;
    white-space: nowrap;
  }

  .status {
    flex: none;
    width: 6.5rem;
    text-align: right;
    font-size: 0.9rem;
    color: var(--muted);
  }

  .status.needs_review,
  .review,
  .error {
    color: var(--danger);
  }

  .empty {
    color: var(--muted);
    text-align: center;
    padding: 2rem 0;
  }

  @media (max-width: 30rem) {
    .status {
      display: none;
    }
  }
</style>
