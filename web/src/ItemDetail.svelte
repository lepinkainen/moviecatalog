<script lang="ts">
  import { onMount } from 'svelte';
  import { getItem, listRuns, statusLabel, thumb, type Item, type Run } from './api';
  import ReviewPanel from './ReviewPanel.svelte';
  import SeasonList from './SeasonList.svelte';
  import SeriesList from './SeriesList.svelte';

  let { id }: { id: number } = $props();

  let item = $state<Item | null>(null);
  let runs = $state<Run[]>([]);
  let error = $state<string | null>(null);

  async function load() {
    try {
      [item, runs] = await Promise.all([getItem(id), listRuns(id)]);
      error = null;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  onMount(() => {
    load();
    // Keep watching while identification is still running.
    const timer = setInterval(() => {
      if (item?.status === 'captured') load();
    }, 4000);
    return () => clearInterval(timer);
  });

  // Stored requests and responses are JSON text; indent them when they parse.
  function pretty(text: string | null): string {
    if (!text) return '';
    try {
      return JSON.stringify(JSON.parse(text), null, 2);
    } catch {
      return text;
    }
  }

  function when(iso: string): string {
    return new Date(iso).toLocaleString();
  }

  const priced = $derived(runs.filter((r) => r.cost_usd !== null));
  const totalCost = $derived(priced.reduce((sum, r) => sum + (r.cost_usd ?? 0), 0));

  function usd(n: number): string {
    return `$${n.toFixed(4)}`;
  }

  // Each series once, even on a box set of the whole series, with the movies on this disc.
  const series = $derived.by(() => {
    const onDisc = new Map<number, number[]>();
    for (const t of item?.titles ?? []) {
      if (!t.series || !t.tmdb_id) continue;
      onDisc.set(t.series.id, [...(onDisc.get(t.series.id) ?? []), t.tmdb_id]);
    }
    return [...onDisc].map(([id, current]) => ({ id, current }));
  });

  function tmdbURL(mediaType: string, tmdbID: number): string {
    return `https://www.themoviedb.org/${mediaType}/${tmdbID}`;
  }
</script>

<main>
  <a class="back" href="#/items">← Catalogue</a>

  {#if error}
    <p class="error" role="alert">Could not load item #{id}: {error}</p>
  {:else if !item}
    <p class="muted">Loading…</p>
  {:else}
    <header>
      <h1>
        {#if item.titles.length === 0}
          <span class="muted">Not identified yet</span>
        {:else}
          {#each item.titles as t, i (t.id)}
            {#if i > 0}<span class="muted"> / </span>{/if}{t.title}{#if t.year}&nbsp;<span class="muted">({t.year})</span>{/if}
          {/each}
        {/if}
      </h1>
      <p class="muted">Item #{item.id} · captured {when(item.captured_at)}</p>
    </header>

    <div class="photos">
      {#each item.images as image (image.id)}
        <a href={image.url} target="_blank" rel="noopener" title="Open the original photo">
          <img src={thumb(image, 1200)} alt="{image.kind} cover" />
        </a>
      {/each}
    </div>

    <dl>
      <dt>Status</dt>
      <dd>{statusLabel(item.status)}</dd>
      <dt>Format</dt>
      <dd>{item.format ?? '—'}</dd>
      <dt>Confidence</dt>
      <dd>{item.confidence ?? '—'}</dd>
      <dt>Barcode</dt>
      <dd>{item.barcode ?? 'not read'}</dd>
      <dt>Notes</dt>
      <dd>{item.notes || '—'}</dd>
      <dt>Titles</dt>
      <dd>
        {#each item.titles as t (t.id)}
          <div>
            {t.title}
            {#if t.original_title && t.original_title !== t.title}<span class="muted">({t.original_title})</span>{/if}
            · {t.media_type === 'tv' ? 'series' : 'movie'}{#if t.year}, {t.year}{/if} ·
            {#if t.tmdb_id}
              <a href={tmdbURL(t.media_type, t.tmdb_id)} target="_blank" rel="noopener">TMDB {t.tmdb_id}</a>
            {:else}
              <span class="review">no TMDB match</span>
            {/if}
            {#if t.series}
              · <a href="#/library?q={encodeURIComponent(t.series.name)}">{t.series.name}</a>
            {/if}
          </div>
        {:else}
          —
        {/each}
      </dd>
    </dl>

    {#each series as s (s.id)}
      <div class="panel">
        <SeriesList id={s.id} current={s.current} />
      </div>
    {/each}
    {#each item.titles.filter((t) => t.media_type === 'tv' && t.tmdb_id) as t (t.id)}
      <!-- Reloaded when the seasons change, so the copies are current. -->
      {#key t.seasons.join(',')}
        <div class="panel">
          <SeasonList id={t.tmdb_id!} current={t.seasons} />
        </div>
      {/key}
    {/each}

    <!-- Recreated whenever the stored item changes, so the form never shows stale values. -->
    {#key `${item.status}|${item.format}|${item.notes}|${item.titles.map((t) => `${t.id}:${t.seasons.join('.')}`).join(',')}`}
      <ReviewPanel {item} onchanged={load} />
    {/key}

    <section>
      <h2>Identification log</h2>
      {#if priced.length > 0}
        <p class="muted">Estimated cost: {usd(totalCost)} over {priced.length} LLM call{priced.length === 1 ? '' : 's'}</p>
      {/if}
      {#each runs as run (run.id)}
        <details>
          <summary>
            <span class="step">{run.step}</span>
            {#if run.model}<span class="muted">{run.model}</span>{/if}
            {#if run.input_tokens !== null}
              <span class="muted">{run.input_tokens} in / {run.output_tokens} out</span>
            {/if}
            {#if run.cost_usd !== null}<span class="muted">{usd(run.cost_usd)}</span>{/if}
            {#if run.error}<span class="review">failed</span>{/if}
            <span class="muted time">{when(run.created_at)}</span>
          </summary>
          {#if run.error}<p class="review">{run.error}</p>{/if}
          {#if run.request}
            <h3>Request</h3>
            <pre>{pretty(run.request)}</pre>
          {/if}
          {#if run.response}
            <h3>Response</h3>
            <pre>{pretty(run.response)}</pre>
          {/if}
        </details>
      {:else}
        <p class="muted">No identification has run yet.</p>
      {/each}
    </section>
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

  .back {
    color: var(--muted);
    text-decoration: none;
    align-self: flex-start;
  }

  h1 {
    margin: 0;
    font-size: 1.5rem;
    overflow-wrap: anywhere;
  }

  h2 {
    margin: 0 0 0.5rem;
    font-size: 1.1rem;
  }

  h3 {
    margin: 0.75rem 0 0.25rem;
    font-size: 0.9rem;
    color: var(--muted);
  }

  p {
    margin: 0;
  }

  .muted {
    color: var(--muted);
    font-weight: 400;
  }

  .review,
  .error {
    color: var(--danger);
  }

  .photos {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 0.75rem;
  }

  .photos img {
    display: block;
    width: 100%;
    aspect-ratio: 3 / 4;
    object-fit: contain;
    background: var(--surface);
    border-radius: 0.75rem;
  }

  dl {
    margin: 0;
    display: grid;
    grid-template-columns: max-content minmax(0, 1fr);
    gap: 0.4rem 1rem;
    padding: 1rem;
    background: var(--surface);
    border-radius: 0.75rem;
  }

  dt {
    color: var(--muted);
  }

  .panel {
    padding: 1rem;
    background: var(--surface);
    border-radius: 0.75rem;
  }

  dd {
    margin: 0;
    overflow-wrap: anywhere;
  }

  dd a {
    color: var(--link);
  }

  details {
    background: var(--surface);
    border-radius: 0.75rem;
    padding: 0.6rem 0.9rem;
    margin-bottom: 0.5rem;
  }

  summary {
    cursor: pointer;
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem;
    align-items: baseline;
  }

  .step {
    font-weight: 600;
  }

  .time {
    margin-left: auto;
    font-size: 0.85rem;
  }

  pre {
    margin: 0;
    padding: 0.75rem;
    background: var(--bg);
    border-radius: 0.5rem;
    font-size: 0.8rem;
    max-height: 24rem;
    overflow: auto;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
</style>
