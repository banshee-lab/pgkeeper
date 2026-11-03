<script lang="ts">
	import { Heading } from '@runwisp/ui';
	import type { Backup } from '$lib/api/types';
	import { cronText, redactUrl } from '$lib/format';
	import { backups } from '$lib/stores/backups.svelte';
	import NextRun from './NextRun.svelte';
	import StatusBullet from './StatusBullet.svelte';

	let { backup }: { backup: Backup } = $props();
	const status = $derived(backups.statusOf(backup));

	function retentionLabel(backup: Backup): string {
		const labels = [
			backup.restic.keep_daily ? `daily ${backup.restic.keep_daily}` : 'daily —',
			backup.restic.keep_weekly ? `weekly ${backup.restic.keep_weekly}` : 'weekly —',
			backup.restic.keep_monthly ? `monthly ${backup.restic.keep_monthly}` : 'monthly —',
			backup.restic.keep_yearly ? `yearly ${backup.restic.keep_yearly}` : 'yearly —'
		];
		return labels.join(' / ');
	}
</script>

<section class="panel">
	<div class="mb-4 flex items-center justify-between gap-3">
		<Heading level={2} size="sm">Details</Heading>
		<span class="inline-flex items-center gap-2 font-mono text-xs text-on-surface-muted">
			<StatusBullet {status} />
			{status === 'running' ? 'Running' : status === 'success' ? 'Success' : status === 'error' ? 'Failed' : 'Never run'}
		</span>
	</div>

	<div class="grid gap-5 md:grid-cols-2">
		<dl class="space-y-3">
			<dt class="font-mono text-[0.66rem] uppercase tracking-[0.14em] text-on-surface-faint">Source</dt>
			<div class="grid gap-y-2">
				<div>
					<dt class="text-xs text-on-surface-muted">Database</dt>
					<dd class="font-mono text-sm text-on-surface">{backup.database}</dd>
				</div>
				<div>
					<dt class="text-xs text-on-surface-muted">Container</dt>
					<dd class="font-mono text-sm text-on-surface">{backup.container}</dd>
				</div>
				<div>
					<dt class="text-xs text-on-surface-muted">Host:port</dt>
					<dd class="font-mono text-sm text-on-surface">{backup.host}:{backup.port}</dd>
				</div>
				<div>
					<dt class="text-xs text-on-surface-muted">Username</dt>
					<dd class="font-mono text-sm text-on-surface">{backup.username}</dd>
				</div>
			</div>
		</dl>

		<dl class="space-y-3">
			<dt class="font-mono text-[0.66rem] uppercase tracking-[0.14em] text-on-surface-faint">Schedule</dt>
			<div class="grid gap-y-2">
				<div>
					<dt class="text-xs text-on-surface-muted">Cron</dt>
					<dd class="font-mono text-sm text-on-surface">{backup.cron}</dd>
				</div>
				<div>
					<dt class="text-xs text-on-surface-muted">Human readable</dt>
					<dd class="text-sm text-on-surface">{cronText(backup.cron)}</dd>
				</div>
				<div>
					<dt class="text-xs text-on-surface-muted">Next run</dt>
					<NextRun value={backup.next_run} />
				</div>
				<div>
					<dt class="text-xs text-on-surface-muted">Roles dump</dt>
					<dd class="text-sm text-on-surface">{backup.roles ? 'Yes' : 'No'}</dd>
				</div>
			</div>
		</dl>

		<dl class="space-y-3 md:col-span-2">
			<dt class="font-mono text-[0.66rem] uppercase tracking-[0.14em] text-on-surface-faint">Destination</dt>
			<div class="grid gap-y-2 md:grid-cols-2">
				<div>
					<dt class="text-xs text-on-surface-muted">Restic repository</dt>
					<dd class="font-mono text-sm text-on-surface">{redactUrl(backup.restic.repository)}</dd>
				</div>
				<div>
					<dt class="text-xs text-on-surface-muted">Tag</dt>
					<dd class="font-mono text-sm text-on-surface">{backup.restic.tag || '—'}</dd>
				</div>
				<div class="md:col-span-2">
					<dt class="text-xs text-on-surface-muted">Retention</dt>
					<dd class="font-mono text-sm text-on-surface">{retentionLabel(backup)}</dd>
				</div>
			</div>
		</dl>
	</div>
</section>
