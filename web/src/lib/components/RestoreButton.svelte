<script lang="ts">
	import { goto } from '$app/navigation';
	import { Button, Input, toast } from '@runwisp/ui';
	import { ApiError, listRestoreSnapshots, startContainerRestore, startTestRestore } from '#lib/api/client';
	import type { Activity, Backup, RestoreSnapshot } from '#lib/api/types';
	import { formatDateTime } from '#lib/format';
	import { activities } from '#lib/stores/activities.svelte';

	let { backup }: { backup: Backup } = $props();

	let dialog = $state<HTMLDialogElement>();
	let step = $state<'choose' | 'container'>('choose');
	let busy = $state(false);
	let snapshots = $state<RestoreSnapshot[]>([]);
	let snapshotsLoading = $state(false);
	let snapshotsError = $state<string | null>(null);
	let container = $state('');
	let database = $state('');
	let snapshotId = $state('');
	let includeRoles = $state(false);

	const selected = $derived(snapshots.find((snapshot) => snapshot.id === snapshotId) ?? null);
	const rolesAvailable = $derived(Boolean(selected?.has_roles));

	function open() {
		step = 'choose';
		dialog?.showModal();
	}

	function close() {
		dialog?.close();
	}

	async function openContainerForm() {
		step = 'container';
		container = backup.container;
		database = backup.database;
		includeRoles = false;
		snapshotsLoading = true;
		snapshotsError = null;
		try {
			// Most recent first, as returned by the API.
			snapshots = await listRestoreSnapshots(backup.name);
			snapshotId = snapshots[0]?.id ?? '';
		} catch (reason) {
			snapshots = [];
			snapshotId = '';
			snapshotsError = reason instanceof Error ? reason.message : 'Could not load snapshots.';
		} finally {
			snapshotsLoading = false;
		}
	}

	async function started(activity: Activity, message: string) {
		activities.upsert(activity);
		close();
		await goto(`/backups/${encodeURIComponent(backup.name)}?activity=${encodeURIComponent(activity.id)}`, {
			keepFocus: true,
			noScroll: true
		});
		toast.success(message);
	}

	function failed(error: unknown, fallback: string) {
		if (error instanceof ApiError && error.status === 409) {
			toast.error('A backup or restore of this project is already running.');
		} else {
			toast.error(error instanceof Error ? error.message : fallback);
		}
	}

	async function runTest() {
		if (busy) return;
		busy = true;
		try {
			await started(await startTestRestore(backup.name), 'Test restore started');
		} catch (error) {
			failed(error, 'Could not start the test restore.');
		} finally {
			busy = false;
		}
	}

	async function runContainer() {
		if (busy || !snapshotId) return;
		busy = true;
		try {
			const activity = await startContainerRestore(backup.name, {
				snapshot_id: snapshotId,
				container: container.trim(),
				database: database.trim(),
				include_roles: includeRoles && rolesAvailable
			});
			await started(activity, 'Restore started');
		} catch (error) {
			failed(error, 'Could not start the restore.');
		} finally {
			busy = false;
		}
	}
</script>

<Button type="button" variant="secondary" size="sm" onclick={open}>Restore</Button>

<dialog
	bind:this={dialog}
	class="m-auto w-full max-w-lg rounded border border-outline bg-surface p-5 text-on-surface backdrop:bg-black/50"
	aria-label="Restore {backup.name}"
>
	{#if step === 'choose'}
		<h2 class="text-base font-semibold">Restore {backup.name}</h2>
		<div class="mt-4 space-y-3">
			<button
				type="button"
				class="w-full rounded border border-outline px-4 py-3 text-left hover:bg-surface-sunken disabled:opacity-50"
				disabled={busy || !backup.restore_image}
				onclick={runTest}
			>
				<span class="block text-sm font-medium">Test Restore</span>
				<span class="block text-xs text-on-surface-muted">
					{#if backup.restore_image}
						Restore the latest snapshot into a temporary container ({backup.restore_image}) that is removed afterwards.
					{:else}
						No restore image configured. Set <span class="font-mono">restore_image</span> in the project or in [restore].
					{/if}
				</span>
			</button>
			<button
				type="button"
				class="w-full rounded border border-outline px-4 py-3 text-left hover:bg-surface-sunken disabled:opacity-50"
				disabled={busy}
				onclick={openContainerForm}
			>
				<span class="block text-sm font-medium">Restore to Container</span>
				<span class="block text-xs text-on-surface-muted">
					Restore a chosen snapshot into the source or another container and database.
				</span>
			</button>
		</div>
		<div class="mt-5 flex justify-end">
			<Button type="button" variant="secondary" size="sm" onclick={close}>Cancel</Button>
		</div>
	{:else}
		<h2 class="text-base font-semibold">Restore to Container</h2>
		<div class="mt-4 space-y-4">
			<div class="space-y-1">
				<span class="text-xs text-on-surface-muted">Target container</span>
				<Input bind:value={container} aria-label="Target container" size="sm" />
			</div>
			<div class="space-y-1">
				<span class="text-xs text-on-surface-muted">Target database</span>
				<Input bind:value={database} aria-label="Target database" size="sm" />
			</div>
			<div class="space-y-1">
				<label class="text-xs text-on-surface-muted" for="restore-snapshot">Snapshot (most recent first)</label>
				{#if snapshotsLoading}
					<p class="text-sm text-on-surface-muted">Loading snapshots…</p>
				{:else if snapshotsError}
					<p class="text-sm text-danger-soft-text">{snapshotsError}</p>
				{:else if snapshots.length === 0}
					<p class="text-sm text-on-surface-muted">No snapshots available yet.</p>
				{:else}
					<select
						id="restore-snapshot"
						class="w-full rounded border border-outline bg-surface px-2 py-1.5 font-mono text-sm"
						bind:value={snapshotId}
					>
						{#each snapshots as snapshot (snapshot.id)}
							<option value={snapshot.id}>{formatDateTime(snapshot.timestamp)} · {snapshot.short_id}</option>
						{/each}
					</select>
				{/if}
			</div>
			<label class="flex items-center gap-2 text-sm" class:opacity-50={!rolesAvailable}>
				<input type="checkbox" bind:checked={includeRoles} disabled={!rolesAvailable} />
				Also restore database roles first (existing roles are overwritten)
			</label>
			<p class="rounded border border-outline bg-surface-sunken px-3 py-2 text-xs text-danger-soft-text">
				The database <span class="font-mono">{database || '…'}</span> in container
				<span class="font-mono">{container || '…'}</span> is dropped and recreated; its current data is lost.
			</p>
		</div>
		<div class="mt-5 flex justify-between gap-2">
			<Button type="button" variant="secondary" size="sm" onclick={() => (step = 'choose')}>Back</Button>
			<div class="flex gap-2">
				<Button type="button" variant="secondary" size="sm" onclick={close}>Cancel</Button>
				<Button
					type="button"
					variant="primary"
					size="sm"
					loading={busy}
					disabled={busy || !snapshotId || !container.trim() || !database.trim()}
					onclick={runContainer}
				>
					Restore
				</Button>
			</div>
		</div>
	{/if}
</dialog>
