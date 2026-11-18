<script lang="ts">
  import { createWant, getSeries, posterURL, type Series, type SeriesLoader, type SeriesPart } from './api';

  /**
   * id: the TMDB collection. current: the TMDB ids of the movies being viewed, to highlight them.
   * load: where the series comes from; the list hides itself when it gives null.
   * readonly: the static export, with no wishlist.
   */
  let {
    id,
    current = [],
    load = getSeries,
    readonly = false,
  }: { id: number; current?: number[]; load?: SeriesLoader; readonly?: boolean } = $props();

  let series = $state<Series | null>(null);
  let missing = $state(false);
  let error = $state<string | null>(null);

  $effect(() => {
    const wanted = id;
    series = null;
    missing = false;
    error = null;
    load(wanted).then(
      (s) => {
        if (wanted !== id) return;
        series = s;
        missing = s === null;
      },
      (e) => {
        if (wanted === id) error = e instanceof Error ? e.message : String(e);
      },
    );
  });

  // TMDB ids being added to the wishlist, and why an addition failed.
  let adding = $state<number[]>([]);
  let failed = $state<Record<number, string>>({});

  async function wish(part: SeriesPart) {
    adding.push(part.tmdb_id);
    delete failed[part.tmdb_id];
    try {
      await createWant({
        tmdb_id: part.tmdb_id,
        media_type: 'movie',
        title: part.title,
        original_title: null,
        year: part.year,
        poster_path: part.poster_path,
      });
      part.wanted = true;
    } catch (e) {
      failed[part.tmdb_id] = e instanceof Error ? e.message : String(e);
    } finally {
      adding = adding.filter((id) => id !== part.tmdb_id);
    }
  }

  const released = $derived(series?.parts.filter((p) => p.released) ?? []);
  const owned = $derived(released.filter((p) => p.copies.length > 0).length);
</script>

{#snippet wishButton(part: SeriesPart, upgrade: boolean)}
  <button
    class="wish"
    class:failed={failed[part.tmdb_id]}
    title={failed[part.tmdb_id]
      ? `Could not add to the wishlist: ${failed[part.tmdb_id]}`
      : upgrade
        ? 'Add a Blu-ray/UHD upgrade to the wishlist'
        : 'Add to wishlist'}
    aria-label="Add {upgrade ? 'an upgrade of ' : ''}{part.title} to the wishlist"
    disabled={adding.includes(part.tmdb_id)}
    onclick={() => wish(part)}>+</button
  >
{/snippet}

{#if !missing}
<section class="series">
  {#if error}
    <p class="muted">Could not load the series: {error}</p>
  {:else if !series}
    <p class="muted">Loading series…</p>
  {:else}
    <h3>
      <a href="#/library?q={encodeURIComponent(series.name)}" title="Show the series in the library">{series.name}</a>
      <span class="muted">· {owned} of {released.length} owned</span>
    </h3>
    <ol>
      {#each series.parts as part (part.tmdb_id)}
        {@const poster = posterURL(part.poster_path, 92)}
        {@const best = part.copies[0]}
        <li class:current={current.includes(part.tmdb_id)} class:missing={part.copies.length === 0}>
          {#if poster}<img src={poster} alt="" loading="lazy" />{:else}<span class="noposter"></span>{/if}
          <span class="name">
            {#if part.title_id !== null && !current.includes(part.tmdb_id)}
              <a href="#/library/{part.title_id}">{part.title}</a>
            {:else}
              {part.title}
            {/if}
            <span class="muted">{part.year ?? 'date not announced'}</span>
          </span>
          <span class="state">
            {#if best}
              {best.format ?? 'owned'}{#if part.copies.length > 1}<span class="muted"> ×{part.copies.length}</span>{/if}
              {#if best.format === 'DVD' && !readonly}
                {#if part.wanted}
                  <a class="muted" href="#/wishlist">upgrade on wishlist</a>
                {:else}
                  {@render wishButton(part, true)}
                {/if}
              {/if}
            {:else if !part.released}
              <span class="muted">upcoming</span>
            {:else if part.wanted && !readonly}
              <a class="muted" href="#/wishlist">on wishlist</a>
            {:else}
              <span class="muted">not owned</span>
              {#if !readonly}{@render wishButton(part, false)}{/if}
            {/if}
          </span>
        </li>
      {/each}
    </ol>
  {/if}
</section>
{/if}

<style>
  .series {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  h3 {
    margin: 0;
    font-size: 1rem;
  }

  p {
    margin: 0;
  }

  a {
    color: var(--link);
  }

  .muted {
    color: var(--muted);
    font-weight: 400;
  }

  ol {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
  }

  li {
    display: grid;
    grid-template-columns: 2rem minmax(0, 1fr) auto;
    gap: 0.6rem;
    align-items: center;
    padding: 0.25rem 0.4rem;
    border-radius: 0.4rem;
  }

  li.current {
    background: var(--bg);
  }

  li.missing img {
    opacity: 0.45;
  }

  img,
  .noposter {
    display: block;
    width: 2rem;
    aspect-ratio: 2 / 3;
    object-fit: cover;
    border-radius: 0.2rem;
    background: var(--bg);
  }

  .name {
    display: flex;
    flex-direction: column;
    min-width: 0;
    overflow-wrap: anywhere;
    line-height: 1.25;
  }

  .name .muted {
    font-size: 0.85rem;
  }

  .state {
    font-size: 0.9rem;
    white-space: nowrap;
    display: flex;
    align-items: center;
    gap: 0.35rem;
  }

  .state a.muted {
    color: var(--muted);
  }

  .wish {
    width: 1.5rem;
    height: 1.5rem;
    padding: 0;
    border: 1px solid var(--muted);
    border-radius: 50%;
    background: none;
    color: var(--muted);
    font: inherit;
    font-size: 0.9rem;
    line-height: 1;
    cursor: pointer;
  }

  .wish:hover,
  .wish:focus-visible {
    color: var(--link);
    border-color: var(--link);
  }

  .wish:disabled {
    cursor: progress;
  }

  .wish.failed {
    color: var(--danger);
    border-color: var(--danger);
  }
</style>
