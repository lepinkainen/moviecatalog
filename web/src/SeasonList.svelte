<script lang="ts">
  import { createWant, getShow, posterURL, type Show, type ShowLoader, type ShowSeason } from './api';

  /**
   * id: the TMDB series. current: the seasons on the disc being viewed, to highlight them.
   * load: where the seasons come from; the list hides itself when it gives null.
   * readonly: the static export, with no wishlist.
   */
  let {
    id,
    current = [],
    load = getShow,
    readonly = false,
  }: { id: number; current?: number[]; load?: ShowLoader; readonly?: boolean } = $props();

  let show = $state<Show | null>(null);
  let missing = $state(false);
  let error = $state<string | null>(null);

  $effect(() => {
    const wanted = id;
    show = null;
    missing = false;
    error = null;
    load(wanted).then(
      (s) => {
        if (wanted !== id) return;
        show = s;
        missing = s === null;
      },
      (e) => {
        if (wanted === id) error = e instanceof Error ? e.message : String(e);
      },
    );
  });

  // Season numbers being added to the wishlist, and why an addition failed.
  let adding = $state<number[]>([]);
  let failed = $state<Record<number, string>>({});

  async function wish(s: Show, season: ShowSeason) {
    adding.push(season.number);
    delete failed[season.number];
    try {
      await createWant(
        {
          tmdb_id: s.tmdb_id,
          media_type: 'tv',
          title: s.name,
          original_title: s.original_name || null,
          year: s.year,
          poster_path: s.poster_path,
        },
        undefined,
        season.number,
      );
      season.wanted = true;
    } catch (e) {
      failed[season.number] = e instanceof Error ? e.message : String(e);
    } finally {
      adding = adding.filter((n) => n !== season.number);
    }
  }

  // Specials do not count towards "owned of".
  const released = $derived(show?.seasons.filter((s) => s.released && s.number > 0) ?? []);
  const owned = $derived(released.filter((s) => s.copies.length > 0).length);
  const ended = $derived(show?.status === 'Ended' || show?.status === 'Canceled');
</script>

{#if !missing}
<section class="seasons">
  {#if error}
    <p class="muted">Could not load the seasons: {error}</p>
  {:else if !show}
    <p class="muted">Loading seasons…</p>
  {:else}
    <h3>
      Seasons
      <span class="muted">· {owned} of {released.length} owned{#if show.status && !ended} · {show.status.toLowerCase()}{/if}</span>
    </h3>
    <ol>
      {#each show.seasons as season (season.number)}
        {@const poster = posterURL(season.poster_path ?? show.poster_path, 92)}
        {@const best = season.copies[0]}
        <li class:current={current.includes(season.number)} class:missing={season.copies.length === 0}>
          {#if poster}<img src={poster} alt="" loading="lazy" />{:else}<span class="noposter"></span>{/if}
          <span class="name">
            {season.name}
            <span class="muted">
              {season.year ?? 'date not announced'}{#if season.episode_count > 0}
                · {season.episode_count} episode{season.episode_count === 1 ? '' : 's'}{/if}
            </span>
          </span>
          <span class="state">
            {#if best}
              {best.format ?? 'owned'}{#if season.copies.length > 1}<span class="muted"> ×{season.copies.length}</span>{/if}
            {:else if !season.released}
              <span class="muted">upcoming</span>
            {:else if season.wanted && !readonly}
              <a class="muted" href="#/wishlist">on wishlist</a>
            {:else}
              <span class="muted">not owned</span>
              {#if !readonly}<button
                class="wish"
                class:failed={failed[season.number]}
                title={failed[season.number]
                  ? `Could not add to the wishlist: ${failed[season.number]}`
                  : 'Add to wishlist'}
                aria-label="Add {season.name} of {show.name} to the wishlist"
                disabled={adding.includes(season.number)}
                onclick={() => show && wish(show, season)}>+</button
              >{/if}
            {/if}
          </span>
        </li>
      {/each}
    </ol>
    {#if show.unsorted.length > 0}
      <p class="muted small">
        {show.unsorted.length} disc{show.unsorted.length === 1 ? ' does' : 's do'} not say which seasons
        {show.unsorted.length === 1 ? 'it holds' : 'they hold'}. Tick them under “Correct this item”.
      </p>
    {/if}
  {/if}
</section>
{/if}

<style>
  .seasons {
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

  .small {
    font-size: 0.85rem;
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
