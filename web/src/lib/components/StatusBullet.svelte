<script lang="ts">
	import { StatusDot } from '@runwisp/ui';
	import { statusLabel } from '#lib/format';

	type Status = 'running' | 'success' | 'error' | 'never';

	let { status }: { status: Status } = $props();

	const tone = $derived(
		status === 'running'
			? 'primary'
			: status === 'success'
				? 'success'
				: status === 'error'
					? 'danger'
					: 'muted'
	);
</script>

<span class="inline-flex items-center gap-2" title={statusLabel(status)}>
	<StatusDot {tone} pulse={status === 'running'} />
	<span class="sr-only">{statusLabel(status)}</span>
</span>
