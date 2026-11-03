<script lang="ts">
	import { Button, Heading } from '@runwisp/ui';
	import { page } from '$app/state';

	const title = $derived(page.error?.message ?? 'Something went wrong.');
</script>

<svelte:head>
	<title>Error · pgkeeper</title>
</svelte:head>

<section class="space-y-3 border-t border-outline py-5">
	<Heading level={1} size="xl">Page unavailable</Heading>
	<p class="text-sm text-on-surface-muted">{title}</p>
	<Button variant="secondary" onclick={() => (window.location.href = '/')}>Return to dashboard</Button>
</section>
