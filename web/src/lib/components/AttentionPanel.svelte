<script lang="ts">
	import { Button, Heading } from '@runwisp/ui';
	import { toast } from '@runwisp/ui';
	import { ApiError, runBackup } from '$lib/api/client';
	import type { Backup } from '$lib/api/types';
	import { activityHref, projectHref } from '$lib/dashboard';
	import { relativeShort } from '$lib/format';
	import { activities } from '$lib/stores/activities.svelte';
	import { backups } from '$lib/stores/backups.svelte';

	let { items, now }: { items: Backup[]; now: number } = $props();
	let retrying = $state<string | null>(null);

	async function retry(backup: Backup) {
		if (retrying) return;
		retrying = backup.name;
		try {
			await runBackup(backup.name);
			toast.success(`Backup ${backup.name} queued.`);
			await Promise.all([backups.load(), activities.load()]);
		} catch (error) {
			if (error instanceof ApiError && (error.status === 409 || error.code === 'backup_running')) {
				toast.info('This backup is already running.');
			} else if (error instanceof ApiError && (error.status === 503 || error.code === 'unavailable')) {
				toast.error('pgkeeper is shutting down. Try again shortly.');
			} else {
				toast.error(error instanceof Error ? error.message : 'Could not retry this backup.');
			}
		} finally {
			retrying = null;
		}
	}
</script>

<section class="panel">
	<Heading level={2} size="sm">Needs attention <span class="font-normal text-on-surface-faint">({items.length})</span></Heading>
	{#if items.length === 0}
		<p class="py-4 text-sm text-on-surface-muted">All projects are healthy.</p>
	{:else}
		<ul class="divide-y divide-outline">
			{#each items as backup (backup.name)}
				{@const activity = backup.last_activity}
				<li class="flex flex-wrap items-center justify-between gap-3 py-3">
					<div class="min-w-0 flex-1">
						<a class="font-mono font-medium text-on-surface hover:underline" href={activity ? activityHref(activity) : projectHref(backup.name)}>
							{backup.name}
						</a>
						<p class="mt-1 truncate text-xs text-danger-soft-text">{activity?.error || 'The latest backup failed.'}</p>
						<p class="mt-1 text-xs text-on-surface-muted">{activity ? relativeShort(activity.finished_at, now) : '—'}</p>
					</div>
					<Button
						variant="secondary"
						size="sm"
						loading={retrying === backup.name}
						disabled={retrying !== null}
						onclick={() => retry(backup)}
					>
						Retry
					</Button>
				</li>
			{/each}
		</ul>
	{/if}
</section>
