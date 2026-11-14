<script lang="ts">
  import { getShow, parseSeasonList, seasonsLabel, type Show } from './api';

  /**
   * tmdbId: the series on TMDB, whose seasons become checkboxes; null for a
   * title typed by hand, which gets a text field. seasons: the ticked ones.
   */
  let { tmdbId, seasons = $bindable() }: { tmdbId: number | null; seasons: number[] } = $props();

  let show = $state<Show | null>(null);
  let failed = $state(false);

  $effect(() => {
    const id = tmdbId;
    show = null;
    failed = false;
    if (id === null) return;
    loadShow(id).then(
      (s) => {
        if (id === tmdbId) show = s;
      },
      () => {
        if (id === tmdbId) failed = true;
      },
    );
  });

  // Every season TMDB lists, specials last, plus any ticked season it does not list.
  interface Choice {
    number: number;
    label: string;
    title: string;
    released: boolean;
  }
  const choices = $derived.by((): Choice[] => {
    if (!show) return [];
    const list: Choice[] = show.seasons.map((s) => ({
      number: s.number,
      label: s.number === 0 ? 'Specials' : String(s.number),
      title: `${s.name}${s.year ? ` (${s.year})` : ''}${s.released ? '' : ' · not aired yet'}`,
      released: s.released,
    }));
    for (const n of seasons) {
      if (!list.some((c) => c.number === n)) list.push({ number: n, label: String(n), title: `Season ${n}`, released: true });
    }
    return list.sort((a, b) => (a.number === 0 ? 1 : b.number === 0 ? -1 : a.number - b.number));
  });

  function toggle(n: number, on: boolean) {
    seasons = on ? [...seasons, n].sort((a, b) => a - b) : seasons.filter((s) => s !== n);
  }

  const allAired = () => (seasons = choices.filter((c) => c.released && c.number > 0).map((c) => c.number));

  // The text field keeps what is typed; the seasons follow it.
  let typed = $state(seasonsLabel(seasons)?.replace(/^Seasons? /, '') ?? '');
</script>

<script module lang="ts">
  // One TMDB lookup per series while the page is open, however often the panel is recreated.
  const shows = new Map<number, Promise<Show>>();
  function loadShow(id: number): Promise<Show> {
    let p = shows.get(id);
    if (!p) {
      p = getShow(id);
      p.catch(() => shows.delete(id));
      shows.set(id, p);
    }
    return p;
  }
</script>

<div class="picker">
  {#if show}
    <div class="boxes" role="group" aria-label="Seasons on this disc">
      {#each choices as c (c.number)}
        <label class:unaired={!c.released} title={c.title}>
          <input
            type="checkbox"
            checked={seasons.includes(c.number)}
            onchange={(e) => toggle(c.number, e.currentTarget.checked)}
          />
          {c.label}
        </label>
      {/each}
    </div>
    <button type="button" class="link" onclick={allAired}>All aired</button>
    <button type="button" class="link" onclick={() => (seasons = [])}>None</button>
  {:else if tmdbId === null || failed}
    <input
      type="text"
      placeholder="Seasons, e.g. 1-3, 5"
      aria-label="Seasons on this disc"
      bind:value={typed}
      oninput={() => (seasons = parseSeasonList(typed))}
    />
    {#if failed}<span class="muted">Could not load the seasons from TMDB.</span>{/if}
  {:else}
    <span class="muted">Loading seasons…</span>
  {/if}
</div>

<style>
  .picker {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.35rem 0.75rem;
    font-size: 0.9rem;
  }

  .boxes {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem 0.6rem;
  }

  label {
    display: inline-flex;
    align-items: center;
    gap: 0.2rem;
    cursor: pointer;
  }

  label.unaired {
    color: var(--muted);
  }

  .link {
    padding: 0;
    border: none;
    background: none;
    color: var(--accent);
    font: inherit;
    cursor: pointer;
  }

  .muted {
    color: var(--muted);
  }
</style>
