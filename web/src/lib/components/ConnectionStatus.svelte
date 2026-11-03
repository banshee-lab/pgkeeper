<script lang="ts">
	import { StatusDot } from '@runwisp/ui';
	import type { ConnectionState } from '$lib/stores/connection.svelte';

	let { state }: { state: ConnectionState } = $props();
	const label = $derived(
		state === 'connected' ? 'Connected' : state === 'disconnected' ? 'Disconnected' : 'Connecting…'
	);
	const tone = $derived(
		state === 'connected' ? 'success' : state === 'disconnected' ? 'danger' : 'warning'
	);
</script>

<div class="flex items-center gap-2 px-2 py-3 font-mono text-xs text-on-surface-muted" aria-live="polite">
	<StatusDot {tone} pulse={state === 'connecting'} />
	<span>{label}</span>
</div>
