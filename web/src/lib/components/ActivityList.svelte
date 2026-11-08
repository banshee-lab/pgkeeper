<script lang="ts">
	import { Heading } from '@runwisp/ui';
	import type { Activity } from '#lib/api/types';
	import { formatDateTime, formatDuration, statusLabel, triggerLabel } from '#lib/format';
	import StatusBullet from './StatusBullet.svelte';

	let {
		items,
		selectedId,
		onSelect
	}: {
		items: Activity[];
		selectedId: string | null;
		onSelect: (activityId: string) => void;
	} = $props();
</script>

<section class="panel">
	<Heading level={2} size="sm">Activity</Heading>
	{#if items.length === 0}
		<p class="py-4 text-sm text-on-surface-muted">No activities yet.</p>
	{:else}
		<ul class="mt-3 space-y-2">
			{#each items as activity (activity.id)}
				<li>
					<button
						type="button"
						class="w-full rounded-[4px] border px-3 py-3 text-left transition-colors"
						class:border-outline={activity.id !== selectedId}
						class:border-primary={activity.id === selectedId}
						class:bg-surface-sunken={activity.id === selectedId}
						onclick={() => onSelect(activity.id)}
						aria-current={activity.id === selectedId ? 'true' : undefined}
					>
						<div class="flex items-start justify-between gap-3">
							<div class="flex min-w-0 items-center gap-2">
								<StatusBullet status={activity.status} />
								<div class="min-w-0">
									<div class="flex items-center gap-2">
										{#if activity.kind && activity.kind !== 'backup'}
											<span class="rounded bg-surface-sunken px-1.5 py-0.5 font-mono text-[10px] text-on-surface">{activity.label ?? 'Restore'}</span>
										{/if}
										<span class="font-mono text-xs text-on-surface-muted">{statusLabel(activity.status)}</span>
										{#if activity.status === 'running'}
											<span class="rounded bg-primary/10 px-1.5 py-0.5 font-mono text-[10px] text-primary">Running</span>
										{/if}
									</div>
									<p class="mt-1 truncate text-sm text-on-surface">{formatDateTime(activity.started_at)}</p>
								</div>
							</div>
							<div class="text-right text-[11px] text-on-surface-muted">
								<div>{triggerLabel(activity.triggered_by)}</div>
								<div class="mt-1 font-mono">{activity.status === 'running' ? '—' : formatDuration(activity.duration_ms)}</div>
							</div>
						</div>
						{#if activity.error}
							<p class="mt-2 max-w-xl truncate text-xs text-danger-soft-text" title={activity.error}>{activity.error}</p>
						{/if}
					</button>
				</li>
			{/each}
		</ul>
	{/if}
</section>
