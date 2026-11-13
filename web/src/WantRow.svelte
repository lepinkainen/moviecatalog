<script lang="ts">
  import { untrack } from 'svelte';
  import { WANT_FORMATS, ago, deleteWant, euros, posterURL, updateWant, type Offer, type Want } from './api';

  let { want, onchanged }: { want: Want; onchanged: () => void } = $props();

  /** Offers shown before "show all". */
  const FIRST_OFFERS = 4;

  // The parent recreates this row when the stored wish changes (keyed on its
  // fields), so the form starts from it once.
  const initial = untrack(() => want);
  let formats = $state<string[]>([...initial.formats]);
  let cap = $state(initial.max_price_cents === null ? '' : (initial.max_price_cents / 100).toFixed(2).replace('.', ','));
  let notes = $state(initial.notes);
  let showAll = $state(false);
  let busy = $state(false);
  let error = $state<string | null>(null);

  /** Cents from "12,50", "12.5" or "12"; null for empty; NaN when unreadable. */
  function parseCap(text: string): number | null {
    const t = text.trim().replace(',', '.').replace(/\s*€$/, '');
    if (t === '') return null;
    return /^\d+(\.\d{1,2})?$/.test(t) ? Math.round(Number(t) * 100) : NaN;
  }

  const capCents = $derived(parseCap(cap));
  const dirty = $derived(
    formats.join() !== want.formats.join() || capCents !== want.max_price_cents || notes.trim() !== want.notes,
  );
  const valid = $derived(formats.length > 0 && !Number.isNaN(capCents) && (capCents === null || capCents > 0));
  const offers = $derived(want.offers ?? []);

  // "2026-11-11" as a local date, without a time zone shift.
  function day(iso: string): string {
    const [y, m, d] = iso.split('-').map(Number);
    return new Date(y, m - 1, d).toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' });
  }
  const shown = $derived(showAll ? offers : offers.slice(0, FIRST_OFFERS));

  function toggle(format: string) {
    formats = formats.includes(format)
      ? formats.filter((f) => f !== format)
      : WANT_FORMATS.filter((f) => f === format || formats.includes(f));
  }

  async function run(action: () => Promise<unknown>) {
    busy = true;
    error = null;
    try {
      await action();
      onchanged();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }

  const save = () => run(() => updateWant(want.id, { formats, max_price_cents: capCents, notes: notes.trim() }));
  const remove = () => {
    if (confirm(`Remove ${want.title.title} from the wishlist?`)) run(() => deleteWant(want.id));
  };

  function availability(o: Offer): string {
    switch (o.availability) {
      case 'in_stock':
        return 'In stock';
      case 'supplier_order':
        return 'Supplier order';
      case 'preorder':
        return 'Preorder';
      case 'out_of_stock':
        return 'Sold out';
      case 'unknown':
        return 'Stock unknown';
      default:
        return o.availability;
    }
  }
</script>

<li class="row" class:has-deal={offers.some((o) => o.deal)}>
  {#if posterURL(want.title.poster_path, 92)}
    <img class="poster" src={posterURL(want.title.poster_path, 92)} alt="" loading="lazy" />
  {:else}
    <div class="poster none"></div>
  {/if}

  <div class="body">
    <div class="head">
      <h3>
        {want.title.title}
        {#if want.title.year}<span class="year">{want.title.year}</span>{/if}
      </h3>
      {#if want.owned_formats.length > 0}
        <span class="owned" title="Formats already on the shelf">Own: {want.owned_formats.join(', ')}</span>
      {/if}
      {#if want.title.media_type === 'tv' && !want.collected_on}
        <span class="owned">Series: no shop prices</span>
      {/if}
    </div>

    {#if want.collected_on}
      <p class="collected">
        <span class="muted">Collected {day(want.collected_on)}</span>
        <button class="remove" type="button" onclick={remove} disabled={busy} title="Remove from the wishlist"
          >Remove</button
        >
      </p>
    {:else}
      <form
        class="edit"
        onsubmit={(e) => {
          e.preventDefault();
          if (dirty && valid) save();
        }}
      >
        <span class="group" role="group" aria-label="Accepted formats">
          {#each WANT_FORMATS as f (f)}
            <button type="button" aria-pressed={formats.includes(f)} onclick={() => toggle(f)}>{f}</button>
          {/each}
        </span>
        <label class="cap">
          Max
          <input
            bind:value={cap}
            inputmode="decimal"
            placeholder="any"
            aria-invalid={Number.isNaN(capCents)}
            size="6"
          />
          €
        </label>
        <input class="notes" bind:value={notes} placeholder="Notes" />
        {#if dirty}
          <button class="primary" type="submit" disabled={busy || !valid}>Save</button>
        {/if}
        <button class="remove" type="button" onclick={remove} disabled={busy} title="Remove from the wishlist"
          >Remove</button
        >
      </form>
    {/if}
    {#if error}<p class="error">{error}</p>{/if}

    {#if want.offers !== null && want.title.media_type === 'movie' && !want.collected_on}
      {#if offers.length === 0}
        {#if !want.searched_at}
          <p class="muted">Not searched for yet. The shops are searched on the watcher's next refresh.</p>
        {:else}
          <p class="muted">
            {`No shop has it in ${want.formats.join(' or ')} right now` +
              (want.searched_at ? ` (searched ${ago(want.searched_at)}).` : '.')}
          </p>
        {/if}
      {:else}
        <ul class="offers">
          {#each shown as o (o.url + o.format + o.price_cents)}
            <li class:deal={o.deal} class:unavailable={o.availability !== 'in_stock'}>
              <a href={o.url} target="_blank" rel="noopener noreferrer" title="Seen {ago(o.seen_at)}">
                <span class="price">{euros(o.price_cents)}</span>
                {#if o.regular_price_cents && o.regular_price_cents > o.price_cents}
                  <s class="muted">{euros(o.regular_price_cents)}</s>
                {/if}
                <span class="fmt">{o.format}</span>
                <span class="store">{o.store}</span>
                {#if o.condition === 'used'}<span class="tag">Used</span>{/if}
                <span class="avail">{availability(o)}</span>
                <span class="listing muted">{o.title}</span>
              </a>
              {#if o.history_url}
                <a class="history" href={o.history_url} target="_blank" rel="noopener noreferrer" title="Price history"
                  >History</a
                >
              {/if}
            </li>
          {/each}
        </ul>
        {#if want.searched_at}
          <p class="muted small">Shops searched {ago(want.searched_at)}</p>
        {/if}
        {#if offers.length > FIRST_OFFERS}
          <button class="link" type="button" onclick={() => (showAll = !showAll)}>
            {showAll ? 'Show fewer' : `Show all ${offers.length} offers`}
          </button>
        {/if}
      {/if}
    {/if}
  </div>
</li>

<style>
  .row {
    display: grid;
    grid-template-columns: 4rem minmax(0, 1fr);
    gap: 1rem;
    padding: 1rem;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 0.75rem;
  }

  .row.has-deal {
    border-color: var(--accent-2);
    box-shadow: inset 4px 0 0 var(--accent-2);
  }

  .poster {
    width: 4rem;
    aspect-ratio: 2 / 3;
    object-fit: cover;
    border-radius: 0.35rem;
    background: var(--bg);
  }

  .body {
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
    min-width: 0;
  }

  .head {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 0.5rem 0.75rem;
  }

  h3 {
    margin: 0;
    font-size: 1.1rem;
  }

  .year,
  .muted {
    color: var(--muted);
    font-weight: normal;
  }

  .collected {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    margin: 0;
  }

  .owned,
  .tag {
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--muted);
    border: 1px solid var(--border);
    border-radius: 999px;
    padding: 0.05rem 0.5rem;
  }

  .edit {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
  }

  input,
  button {
    font: inherit;
    color: var(--text);
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 0.5rem;
    padding: 0.35rem 0.6rem;
  }

  input[aria-invalid='true'] {
    border-color: var(--danger);
  }

  .cap {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    color: var(--muted);
  }

  .cap input {
    width: 5rem;
    text-align: right;
  }

  .notes {
    flex: 1 1 10rem;
    min-width: 0;
  }

  button {
    cursor: pointer;
  }

  button:disabled {
    cursor: default;
    opacity: 0.6;
  }

  .group {
    display: flex;
    gap: 0.25rem;
  }

  .group button {
    color: var(--muted);
    background: none;
  }

  .group button[aria-pressed='true'] {
    color: var(--on-accent);
    background: var(--accent);
    border-color: var(--accent);
  }

  .primary {
    color: var(--on-accent);
    background: var(--accent);
    border-color: var(--accent);
    font-weight: 600;
  }

  .remove {
    color: var(--danger);
    background: none;
    border-color: transparent;
  }

  .link {
    align-self: flex-start;
    color: var(--accent);
    background: none;
    border: none;
    padding: 0;
  }

  .error {
    margin: 0;
    color: var(--danger);
  }

  p {
    margin: 0;
  }

  .offers {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
  }

  .offers li {
    display: flex;
    align-items: baseline;
    gap: 0.25rem;
  }

  .offers a:first-child {
    flex: 1 1 auto;
    min-width: 0;
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 0.25rem 0.75rem;
    padding: 0.35rem 0.6rem;
    border-radius: 0.5rem;
    color: inherit;
    text-decoration: none;
  }

  .offers a:hover {
    background: var(--bg);
  }

  .offers li.deal a:first-child {
    background: color-mix(in srgb, var(--accent-2) 14%, transparent);
  }

  .offers li.unavailable {
    opacity: 0.7;
  }

  .history {
    font-size: 0.85rem;
    color: var(--accent);
    text-decoration: none;
    padding: 0.35rem 0.4rem;
  }

  .small {
    font-size: 0.85rem;
  }

  .price {
    font-weight: 700;
    font-variant-numeric: tabular-nums;
    min-width: 5.5rem;
  }

  .deal .price {
    color: var(--accent-2);
  }

  .fmt {
    font-weight: 600;
    min-width: 3.5rem;
  }

  .avail {
    font-size: 0.85rem;
    color: var(--muted);
  }

  .listing {
    flex: 1 1 12rem;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 0.85rem;
  }

  @media (max-width: 30rem) {
    .row {
      grid-template-columns: 3rem minmax(0, 1fr);
      gap: 0.75rem;
      padding: 0.75rem;
    }

    .poster {
      width: 3rem;
    }
  }
</style>
