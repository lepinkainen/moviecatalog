<script lang="ts">
  import Library from './Library.svelte';
  import type { LibraryItem, Series, Show } from './api';
  import { parseLibraryHash, type LibraryRoute } from './route';

  /** data.json, written by `moviecatalog export`. */
  interface ExportData {
    title: string;
    generated_at: string;
    items: LibraryItem[];
    /** By TMDB collection id; empty when the export ran without TMDB. */
    series: Record<string, Series>;
    /** By TMDB TV series id; empty (or missing in an older export) without TMDB. */
    shows?: Record<string, Show>;
  }

  // Revalidated, so a new export shows without waiting for a cache to expire.
  const data: Promise<ExportData> = fetch('data.json', { cache: 'no-cache' }).then(async (resp) => {
    if (!resp.ok) throw new Error(`${resp.status} ${resp.statusText}`);
    return resp.json();
  });

  let info = $state<ExportData | null>(null);
  data.then(
    (d) => {
      info = d;
      document.title = d.title;
    },
    () => {}, // the library shows the error
  );

  function parse(): LibraryRoute {
    return parseLibraryHash(location.hash) ?? { selected: null, q: null };
  }

  let route = $state<LibraryRoute>(parse());
</script>

<svelte:window onhashchange={() => (route = parse())} />

<header>
  <h1><a href="#/library">{info?.title ?? ''}</a></h1>
  {#if info}
    <span class="muted">Updated {new Date(info.generated_at).toLocaleDateString()}</span>
  {/if}
</header>

<Library
  selected={route.selected}
  q={route.q}
  readonly
  load={() => data.then((d) => d.items)}
  loadSeries={(id) => data.then((d) => d.series[id] ?? null)}
  loadShow={(id) => data.then((d) => d.shows?.[id] ?? null)}
/>

<footer class="muted">
  Posters and series information from <a href="https://www.themoviedb.org/" target="_blank" rel="noopener">TMDB</a>.
  This product uses the TMDB API but is not endorsed or certified by TMDB.
</footer>

<style>
  header {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 0.25rem 1rem;
    padding: 0.75rem 1rem;
    border-bottom: 1px solid var(--border);
    background: var(--surface);
  }

  h1 {
    margin: 0;
    font-size: 1.25rem;
  }

  h1 a {
    color: inherit;
    text-decoration: none;
  }

  .muted {
    color: var(--muted);
  }

  footer {
    max-width: 100rem;
    margin: 0 auto;
    padding: 1rem 1rem 2rem;
    font-size: 0.85rem;
  }

  footer a {
    color: var(--accent);
  }
</style>
