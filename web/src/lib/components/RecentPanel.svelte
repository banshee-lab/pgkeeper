<script lang="ts">
	import { Heading, StatusDot } from '@runwisp/ui';
	import type { Activity } from '#lib/api/types';
	import { activityHref, repositoryActivityHref, repositoryLabel } from '#lib/dashboard';
	import type { RecentItem } from '#lib/stores/activities.svelte';
	import { formatDuration, relativeShort, statusLabel, triggerLabel } from '#lib/format';

	let { items, now }: { items: RecentItem[]; now: number } = $props();

	function tone(status: Activity['status']) {
		return status === 'success'
			? 'success'
			: status === 'error'
				? 'danger'
				: status === 'warning'
					? 'warning'
					: 'primary';
	}

	function href(item: RecentItem): string {
		return item.type === 'backup'
			? activityHref(item.activity)
			: repositoryActivityHref(item.repository.id, item.activity.id);
	}

	function title(item: RecentItem): string {
		return item.type === 'backup' ? item.activity.backup_name : repositoryLabel(item.repository);
	}

	function action(item: RecentItem): string {
		if (item.type === 'backup') return triggerLabel(item.activity.triggered_by);
		return item.activity.kind.charAt(0).toUpperCase() + item.activity.kind.slice(1);
	}
</script>

<section class="panel">
	<Heading level={2} size="sm">Recent activity</Heading>
	{#if items.length === 0}
		<p class="py-4 text-sm text-on-surface-muted">No activity yet.</p>
	{:else}
		<ul class="divide-y divide-outline">
			{#each items as item (`${item.type}-${item.activity.id}`)}
				{@const activity = item.activity}
				<li>
					<a href={href(item)} class="flex flex-wrap items-center gap-x-3 gap-y-1 py-3 hover:bg-surface-sunken/50">
						<StatusDot tone={tone(activity.status)} pulse={activity.status === 'running'} />
						<span class="min-w-32 flex-1 font-mono font-medium text-on-surface">{title(item)}</span>
						<span class="text-xs text-on-surface-muted">{action(item)}</span>
						<span class="font-mono text-xs text-on-surface-muted">{relativeShort(activity.finished_at ?? activity.started_at, now)}</span>
						<span class="w-16 text-right font-mono text-xs text-on-surface-muted">
							{activity.status === 'running'
								? 'Running'
								: item.type === 'backup'
									? formatDuration(item.activity.duration_ms)
									: '—'}
						</span>
						<span class="sr-only">{statusLabel(activity.status)}</span>
					</a>
				</li>
			{/each}
		</ul>
	{/if}
</section>
