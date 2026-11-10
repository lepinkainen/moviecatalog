<script lang="ts">
  import Capture from './Capture.svelte';
  import Catalogue from './Catalogue.svelte';
  import ItemDetail from './ItemDetail.svelte';

  type Route = { page: 'capture' } | { page: 'catalogue' } | { page: 'item'; id: number };

  function parse(hash: string): Route {
    const item = hash.match(/^#\/items\/(\d+)$/);
    if (item) return { page: 'item', id: Number(item[1]) };
    if (hash === '#/items') return { page: 'catalogue' };
    return { page: 'capture' };
  }

  let route = $state<Route>(parse(location.hash));
</script>

<svelte:window onhashchange={() => (route = parse(location.hash))} />

<nav>
  <a href="#/capture" aria-current={route.page === 'capture' ? 'page' : undefined}>Capture</a>
  <a href="#/items" aria-current={route.page !== 'capture' ? 'page' : undefined}>Catalogue</a>
</nav>

{#if route.page === 'capture'}
  <Capture />
{:else if route.page === 'catalogue'}
  <Catalogue />
{:else}
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
