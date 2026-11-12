<script lang="ts">
  import Capture from './Capture.svelte';
  import Catalogue from './Catalogue.svelte';
  import ItemDetail from './ItemDetail.svelte';
  import Library from './Library.svelte';
  import Wishlist from './Wishlist.svelte';
  import { parseLibraryHash } from './route';

  type Route =
    | { page: 'capture' }
    | { page: 'catalogue' }
    | { page: 'item'; id: number }
    | { page: 'library'; selected: number | null; q: string | null }
    | { page: 'wishlist' };

  function parse(hash: string): Route {
    const item = hash.match(/^#\/items\/(\d+)$/);
    if (item) return { page: 'item', id: Number(item[1]) };
    if (hash === '#/items') return { page: 'catalogue' };
    if (hash === '#/wishlist') return { page: 'wishlist' };
    const library = parseLibraryHash(hash);
    if (library) return { page: 'library', ...library };
    return { page: 'capture' };
  }

  let route = $state<Route>(parse(location.hash));
</script>

<svelte:window onhashchange={() => (route = parse(location.hash))} />

<nav>
  <a href="#/capture" aria-current={route.page === 'capture' ? 'page' : undefined}>Capture</a>
  <a href="#/items" aria-current={route.page === 'catalogue' || route.page === 'item' ? 'page' : undefined}
    >Catalogue</a
  >
  <a href="#/library" aria-current={route.page === 'library' ? 'page' : undefined}>Library</a>
  <a href="#/wishlist" aria-current={route.page === 'wishlist' ? 'page' : undefined}>Wishlist</a>
</nav>

<!-- Capture stays mounted so photos waiting for their other side or for an
     upload survive a visit to the catalogue. -->
<div hidden={route.page !== 'capture'}>
  <Capture active={route.page === 'capture'} />
</div>

{#if route.page === 'catalogue'}
  <Catalogue />
{:else if route.page === 'library'}
  <Library selected={route.selected} q={route.q} />
{:else if route.page === 'wishlist'}
  <Wishlist />
{:else if route.page === 'item'}
  {#key route.id}
    <ItemDetail id={route.id} />
  {/key}
{/if}

<style>
  nav {
    display: flex;
    gap: 0.25rem;
    padding: 0.5rem 1rem;
    border-bottom: 1px solid var(--border);
    background: var(--surface);
  }

  a {
    padding: 0.4rem 0.8rem;
    border-radius: 0.5rem;
    color: var(--muted);
    text-decoration: none;
    font-weight: 600;
  }

  a[aria-current='page'] {
    color: var(--text);
    background: var(--bg);
  }
</style>
