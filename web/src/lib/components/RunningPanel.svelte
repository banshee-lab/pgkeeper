<script lang="ts">
	import { Heading } from '@runwisp/ui';
	import type { Backup } from '#lib/api/types';
	import { activityHref, projectHref } from '#lib/dashboard';
	import { formatDuration, relativeShort, triggerLabel } from '#lib/format';
	import { jobs } from '#lib/stores/jobs.svelte';

	let { items, now }: { items: Backup[]; now: number } = $props();
</script>

<section class="panel">
	<Heading level={2} size="sm">Running now <span class="font-normal text-on-surface-faint">({items.length})</span></Heading>
	{#if items.length === 0}
		<p class="py-4 text-sm text-on-surface-muted">No backups are running.</p>
	{:else}
		<ul class="divide-y divide-outline">
			{#each items as backup (backup.name)}
				{@const activity = backup.last_activity}
				{@const job = jobs.byBackup(backup.name)}
				<li class="flex flex-wrap items-center justify-between gap-3 py-3">
					<div class="min-w-0">
						<a
							class="font-mono font-medium text-on-surface hover:underline"
							href={activity ? activityHref(activity) : projectHref(backup.name)}
						>
							{backup.name}
						</a>
						<p class="mt-1 text-xs text-on-surface-muted">
							{activity ? triggerLabel(activity.triggered_by) : 'Backup'} ·
							{job?.phase ?? 'Phase unavailable'} ·
							{activity ? relativeShort(activity.started_at, now) : 'in progress'}
						</p>
					</div>
					<span class="font-mono text-xs text-on-surface-muted" aria-label="Elapsed time">
						{activity ? formatDuration(now - Date.parse(activity.started_at)) : '—'}
					</span>
				</li>
			{/each}
		</ul>
	{/if}
</section>
