<script lang="ts">
	let { label, value, danger = false }: { label: string; value: string | number; danger?: boolean } =
		$props();
</script>

<div class="min-w-0 border-b border-outline px-3 py-4 sm:border-b-0 sm:border-r">
	<p class="font-mono text-[0.65rem] font-medium uppercase tracking-wider text-on-surface-muted">{label}</p>
	<p class="mt-1 truncate font-mono text-2xl font-semibold tabular-nums {danger ? 'text-danger-soft-text' : 'text-on-surface'}">
		{value}
	</p>
</div>
