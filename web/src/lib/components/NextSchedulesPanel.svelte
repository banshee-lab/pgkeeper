<script lang="ts">
	import { Heading } from '@runwisp/ui';
	import type { UpcomingSchedule } from '#lib/dashboard';
	import { formatDateTime } from '#lib/format';

	let { items }: { items: UpcomingSchedule[] } = $props();
</script>

<section class="panel">
	<Heading level={2} size="sm">Next schedules</Heading>
	{#if items.length === 0}
		<p class="py-4 text-sm text-on-surface-muted">No upcoming schedules.</p>
	{:else}
		<ul class="max-h-96 divide-y divide-outline overflow-y-auto">
			{#each items as item (item.key)}
				<li>
					<a
						href={item.href}
						class="flex flex-wrap items-center gap-x-3 gap-y-1 py-3 hover:bg-surface-sunken/50"
					>
						<span class="min-w-32 flex-1 font-mono font-medium text-on-surface">{item.label}</span>
						<span class="text-xs text-on-surface-muted">{item.detail}</span>
						<time
							class="font-mono text-xs text-on-surface-muted"
							datetime={item.scheduledAt}
						>{formatDateTime(item.scheduledAt)}</time>
					</a>
				</li>
			{/each}
		</ul>
	{/if}
</section>
