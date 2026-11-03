<script lang="ts">
	import { Heading, StatusDot } from '@runwisp/ui';
	import type { Activity } from '$lib/api/types';
	import { activityHref } from '$lib/dashboard';
	import { formatDuration, relativeShort, statusLabel, triggerLabel } from '$lib/format';

	let { items, now }: { items: Activity[]; now: number } = $props();

	function tone(status: Activity['status']) {
		return status === 'success' ? 'success' : status === 'error' ? 'danger' : 'primary';
	}
</script>

<section class="panel">
	<Heading level={2} size="sm">Recent activity</Heading>
	{#if items.length === 0}
		<p class="py-4 text-sm text-on-surface-muted">No backup activity yet.</p>
	{:else}
		<ul class="divide-y divide-outline">
			{#each items as activity (activity.id)}
				<li>
					<a href={activityHref(activity)} class="flex flex-wrap items-center gap-x-3 gap-y-1 py-3 hover:bg-surface-sunken/50">
						<StatusDot tone={tone(activity.status)} pulse={activity.status === 'running'} />
						<span class="min-w-32 flex-1 font-mono font-medium text-on-surface">{activity.backup_name}</span>
						<span class="text-xs text-on-surface-muted">{triggerLabel(activity.triggered_by)}</span>
						<span class="font-mono text-xs text-on-surface-muted">{relativeShort(activity.finished_at ?? activity.started_at, now)}</span>
						<span class="w-16 text-right font-mono text-xs text-on-surface-muted">
							{activity.status === 'running' ? 'Running' : formatDuration(activity.duration_ms)}
						</span>
						<span class="sr-only">{statusLabel(activity.status)}</span>
					</a>
				</li>
			{/each}
		</ul>
	{/if}
</section>
