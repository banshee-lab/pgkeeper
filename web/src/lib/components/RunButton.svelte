<script lang="ts">
	import { goto } from '$app/navigation';
	import { Button, toast } from '@runwisp/ui';
	import { ApiError, listBackupActivities, runBackup } from '#lib/api/client';
	import { activities } from '#lib/stores/activities.svelte';
	import { backups } from '#lib/stores/backups.svelte';

	let { name, running }: { name: string; running: boolean } = $props();
	let loading = $state(false);

	async function handleRun() {
		if (loading || running) return;
		loading = true;
		try {
			const activity = await runBackup(name);
			activities.upsert(activity);
			const existing = backups.list.find((backup) => backup.name === name);
			if (existing) {
				existing.running = true;
				existing.last_activity = activity;
			}
			await goto(`/backups/${encodeURIComponent(name)}?activity=${encodeURIComponent(activity.id)}`, {
				replaceState: false,
				keepFocus: true,
				noScroll: true
			});
			toast.success(`Backup "${name}" started`);
		} catch (error) {
			if (error instanceof ApiError && error.status === 409) {
				try {
					const runningActivities = await listBackupActivities(name, 10);
					const active = runningActivities.find((activity) => activity.status === 'running');
					if (active) {
						await goto(`/backups/${encodeURIComponent(name)}?activity=${encodeURIComponent(active.id)}`, {
							replaceState: true,
							keepFocus: true,
							noScroll: true
						});
						return;
					}
				} catch {
					// Fall through to the generic already-running copy.
				}
				toast.error('Backup is already running.');
				return;
			}
			if (error instanceof ApiError && error.status === 503) {
				toast.error('Server is shutting down. Please try again in a moment.');
				return;
			}
			toast.error(error instanceof Error ? error.message : 'Could not start backup.');
		} finally {
			loading = false;
		}
	}
</script>

<Button type="button" variant="primary" size="sm" disabled={running} loading={loading} onclick={handleRun}>
	Backup now
</Button>
