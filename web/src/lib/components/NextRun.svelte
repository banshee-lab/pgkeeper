<script lang="ts">
	import { serverNow } from '$lib/api/client';
	import { formatDateTime, nextRunUpdateDelay, relativeShort } from '$lib/format';

	let { value }: { value: string | null } = $props();
	let now = $state(serverNow());
	const relative = $derived(relativeShort(value, now));

	$effect(() => {
		const delay = nextRunUpdateDelay(value, now);
		if (delay === null) return;

		const timer = setTimeout(() => {
			now = serverNow();
		}, delay);
		return () => clearTimeout(timer);
	});
</script>

<dd class="text-sm text-on-surface" title={value ? formatDateTime(value) : undefined}>
	{value ? `${relative} · ${formatDateTime(value)}` : '—'}
</dd>
