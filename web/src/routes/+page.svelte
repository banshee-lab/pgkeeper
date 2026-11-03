<script lang="ts">
	import { Button, Heading } from '@runwisp/ui';
	import { toast } from '@runwisp/ui';
	import AttentionPanel from '$lib/components/AttentionPanel.svelte';
	import RecentPanel from '$lib/components/RecentPanel.svelte';
	import RunningPanel from '$lib/components/RunningPanel.svelte';
	import StatTile from '$lib/components/StatTile.svelte';
	import { deriveDashboard } from '$lib/dashboard';
	import { relativeShort } from '$lib/format';
	import { activities } from '$lib/stores/activities.svelte';
	import { backups } from '$lib/stores/backups.svelte';
	import { clock } from '$lib/stores/clock.svelte';

	let refreshing = $state(false);
	const summary = $derived(deriveDashboard(backups.list));
	const running = $derived(backups.list.filter((backup) => backup.running));

	async function refresh() {
		if (refreshing) return;
		refreshing = true;
		await Promise.all([backups.load(), activities.load()]);
		if (backups.state === 'error' || activities.state === 'error') {
			toast.error(backups.error ?? activities.error ?? 'Refresh failed.');
		}
		refreshing = false;
	}
</script>

<svelte:head>
	<title>Dashboard · pgkeeper</title>
</svelte:head>

<div class="space-y-7">
	<div class="flex flex-wrap items-end justify-between gap-3">
		<div>
			<p class="mb-1 font-mono text-[0.65rem] font-semibold uppercase tracking-wider text-on-surface-faint">Operations</p>
			<Heading level={1} size="2xl">Dashboard</Heading>
		</div>
		<Button variant="secondary" size="sm" loading={refreshing} onclick={refresh}>Refresh</Button>
	</div>

	{#if backups.state === 'error' && backups.list.length === 0}
		<div class="border-t border-outline py-4">
			<p class="text-sm text-danger-soft-text">{backups.error}</p>
			<Button variant="secondary" size="sm" class="mt-3" onclick={refresh}>Retry</Button>
		</div>
	{/if}
	{#if activities.state === 'error'}
		<p class="border-t border-outline py-3 text-sm text-danger-soft-text" role="alert">
			Recent activity could not be loaded: {activities.error}
		</p>
	{/if}

	{#if summary.projectCount === 0 && backups.state === 'ready'}
		<div class="border-t border-outline py-5">
			<Heading level={2} size="sm">No projects configured</Heading>
			<p class="mt-2 text-sm text-on-surface-muted">
				Add a backup configuration to see scheduled runs and their activity here.
			</p>
		</div>
	{/if}

	<section aria-label="Backup statistics" class="grid grid-cols-2 border-l border-outline sm:grid-cols-4">
		<StatTile label="Projects" value={summary.projectCount} />
		<StatTile label="Running now" value={summary.runningCount} />
		<StatTile label="Recently failed" value={summary.failed.length} danger={summary.failed.length > 0} />
		<StatTile
			label="Last backup"
			value={summary.lastBackupAt ? relativeShort(summary.lastBackupAt, clock.now) : '—'}
		/>
	</section>

	<div class="grid gap-x-8 gap-y-7 lg:grid-cols-2">
		<RunningPanel items={running} now={clock.now} />
		<AttentionPanel items={summary.failed} now={clock.now} />
	</div>

	<RecentPanel items={activities.recent} now={clock.now} />
</div>
