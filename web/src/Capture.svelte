<script lang="ts">
  import { onMount } from 'svelte';
  import { createItem, deleteItem, listItems, thumb } from './api';
  import { loadPending, removePending, savePending } from './pending';

  // The page stays mounted while hidden; active says whether it is on screen.
  let { active }: { active: boolean } = $props();

  type Capture = {
    key: number;
    state: 'uploading' | 'done' | 'failed' | 'removing';
    thumb: string;
    // Photos are held, here and in browser storage, only until the server has them.
    front?: Blob;
    back?: Blob;
    itemId?: number;
    error?: string;
  };

  // How many finished captures stay visible for a quick sanity check and undo.
  const RECENT = 6;

  let front = $state<Blob | null>(null);
  let frontThumb = $state<string | null>(null);
  let captures = $state<Capture[]>([]);
  let total = $state<number | null>(null);
  let loadError = $state<string | null>(null);
  // Time-based so keys never collide with captures restored from an earlier visit.
  let nextKey = Date.now();

  const side = $derived(front ? 'back' : 'front');
  const unsaved = $derived(captures.filter((c) => c.state === 'uploading' || c.state === 'failed').length);

  // Recount whenever the page is shown: items may have been deleted in the catalogue meanwhile.
  $effect(() => {
    if (!active) return;
    let stale = false;
    listItems().then(
      (items) => {
        if (!stale) {
          total = items.length;
          loadError = null;
          retryFailed(); // the server is reachable again
        }
      },
      (e) => {
        if (!stale) loadError = message(e);
      },
    );
    return () => {
      stale = true;
    };
  });

  // Bring back photos that had not reached the server when the page was last closed.
  onMount(async () => {
    const restored = await loadPending();
    for (const p of restored) {
      if (p.id === 'front') {
        // A photo taken while storage was loading is newer.
        if (!front) {
          front = p.front;
          frontThumb = URL.createObjectURL(p.front);
        }
      } else if (!find(p.id)) {
        captures.push({ key: p.id, state: 'failed', thumb: URL.createObjectURL(p.front), front: p.front, back: p.back });
      }
    }
    captures.sort((a, b) => b.key - a.key);
    retryFailed();
  });

  function retryFailed() {
    for (const c of captures) if (c.state === 'failed') upload(c.key);
  }

  function message(e: unknown): string {
    return e instanceof Error ? e.message : String(e);
  }

  function find(key: number): Capture | undefined {
    return captures.find((c) => c.key === key);
  }

  function onPhoto(event: Event) {
    const input = event.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    // Clear so that picking the same file again still fires a change event.
    input.value = '';
    if (!file) return;

    if (!front) {
      front = file;
      frontThumb = URL.createObjectURL(file);
      savePending({ id: 'front', front: file });
      return;
    }

    const key = nextKey++;
    captures.unshift({ key, state: 'uploading', thumb: frontThumb!, front, back: file });
    // Stored before uploading, so a reload mid-upload loses nothing. IndexedDB
    // applies the two writes in this order, so the front is never in neither.
    savePending({ id: key, front, back: file });
    removePending('front');
    front = null;
    frontThumb = null;
    upload(key);
  }

  function discardFront() {
    if (frontThumb) URL.revokeObjectURL(frontThumb);
    front = null;
    frontThumb = null;
    removePending('front');
  }

  async function upload(key: number) {
    const capture = find(key);
    if (!capture?.front || !capture.back) return;
    capture.state = 'uploading';
    capture.error = undefined;
    try {
      const item = await createItem(capture.front, capture.back);
      URL.revokeObjectURL(capture.thumb);
      const saved = item.images.find((i) => i.kind === 'front');
      capture.thumb = saved ? thumb(saved, 160) : '';
      capture.front = undefined;
      capture.back = undefined;
      capture.itemId = item.id;
      capture.state = 'done';
      removePending(key);
      if (total !== null) total += 1;
      trim();
    } catch (e) {
      capture.state = 'failed';
      capture.error = message(e);
    }
  }

  // Drop finished captures beyond the most recent few.
  function trim() {
    let done = 0;
    captures = captures.filter((c) => c.state !== 'done' || ++done <= RECENT);
  }

  async function undo(key: number) {
    const capture = find(key);
    if (!capture) return;
    if (capture.itemId === undefined) {
      // Never reached the server: nothing to delete there.
      URL.revokeObjectURL(capture.thumb);
      captures = captures.filter((c) => c.key !== key);
      removePending(key);
      return;
    }
    capture.state = 'removing';
    try {
      await deleteItem(capture.itemId);
      captures = captures.filter((c) => c.key !== key);
      if (total !== null) total -= 1;
    } catch (e) {
      capture.state = 'done';
      capture.error = message(e);
    }
  }

  function warnBeforeLeaving(event: BeforeUnloadEvent) {
    if (unsaved > 0 || front) event.preventDefault();
  }
</script>

<svelte:window onbeforeunload={warnBeforeLeaving} ononline={retryFailed} />

<main>
  <header>
    <h1>Capture</h1>
    <p class="count">
      {#if total !== null}
        <strong>{total}</strong> in catalogue
      {:else if loadError}
        <span class="error" role="alert">Server unreachable: {loadError}</span>
      {:else}
        &nbsp;
      {/if}
    </p>
  </header>

  <label class="shoot" class:back={side === 'back'}>
    <input type="file" accept="image/*" capture="environment" onchange={onPhoto} />
    {#if frontThumb}
      <img src={frontThumb} alt="Front cover just taken" />
    {/if}
    <span class="step">{side === 'front' ? '1 of 2' : '2 of 2'}</span>
    <span class="action">Photograph {side}</span>
  </label>

  {#if front}
    <button class="link" onclick={discardFront}>Discard front photo and start over</button>
  {/if}

  {#if captures.length > 0}
    <ul>
      {#each captures as capture (capture.key)}
        <li class={capture.state}>
          <img src={capture.thumb} alt="" />
          <div class="status" role="status">
            {#if capture.state === 'uploading'}
              Uploading…
            {:else if capture.state === 'removing'}
              Removing…
            {:else if capture.state === 'failed'}
              <span class="error">Upload failed: {capture.error}</span>
            {:else}
              Saved as #{capture.itemId}
              {#if capture.error}
                <span class="error">Undo failed: {capture.error}</span>
              {/if}
            {/if}
          </div>
          {#if capture.state === 'failed'}
            <button onclick={() => upload(capture.key)}>Retry</button>
            <button onclick={() => undo(capture.key)}>Discard</button>
          {:else if capture.state === 'done'}
            <button onclick={() => undo(capture.key)}>Undo</button>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</main>

<style>
  main {
    max-width: 32rem;
    margin: 0 auto;
    padding: 1rem;
    padding-bottom: calc(1rem + env(safe-area-inset-bottom));
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  header {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 1rem;
  }

  h1 {
    margin: 0;
    font-size: 1.25rem;
  }

  .count {
    margin: 0;
    color: var(--muted);
  }

  .count strong {
    color: var(--text);
    font-variant-numeric: tabular-nums;
  }

  .shoot {
    position: relative;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 0.25rem;
    min-height: 40vh;
    border-radius: 1rem;
    background: var(--accent);
    color: var(--on-accent);
    cursor: pointer;
    overflow: hidden;
    user-select: none;
    -webkit-tap-highlight-color: transparent;
  }

  .shoot.back {
    background: var(--accent-2);
  }

  .shoot:active {
    filter: brightness(0.9);
  }

  .shoot:focus-within {
    outline: 3px solid var(--text);
    outline-offset: 2px;
  }

  /* Hidden but still focusable and clickable through the label. */
  .shoot input {
    position: absolute;
    width: 1px;
    height: 1px;
    opacity: 0;
  }

  .shoot img {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    object-fit: cover;
    opacity: 0.3;
  }

  .shoot span {
    position: relative;
  }

  .step {
    font-size: 0.9rem;
    opacity: 0.85;
  }

  .action {
    font-size: 1.75rem;
    font-weight: 700;
  }

  ul {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  li {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.5rem;
    border-radius: 0.75rem;
    background: var(--surface);
  }

  li.failed {
    outline: 2px solid var(--danger);
  }

  li img {
    width: 3rem;
    height: 4rem;
    object-fit: cover;
    border-radius: 0.375rem;
    background: var(--bg);
    flex: none;
  }

  .status {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    color: var(--muted);
    overflow-wrap: anywhere;
  }

  .error {
    color: var(--danger);
  }

  button {
    font: inherit;
    color: var(--text);
    background: var(--bg);
    border: 1px solid var(--control-border);
    border-radius: 0.5rem;
    padding: 0.6rem 0.9rem;
    cursor: pointer;
  }

  button.link {
    align-self: center;
    background: none;
    border: none;
    color: var(--muted);
    text-decoration: underline;
  }
</style>
