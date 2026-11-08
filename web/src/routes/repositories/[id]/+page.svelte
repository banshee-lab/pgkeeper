<script lang="ts">
	import { page } from '$app/state';
	import { onDestroy } from 'svelte';
	import { Button, Heading } from '@runwisp/ui';
	import {
		ApiError,
		forceRepositoryUnlock,
		getRepository,
		getRepositorySnapshotStats,
		listBackups,
		listRepositoryActivities,
		listRepositorySnapshots,
		listSnapshotTree,
		startRepositoryActivity
	} from '#lib/api/client';
	import type {
		Repository,
		RepositoryActivity,
		RepositoryNode,
		RepositorySnapshot,
		RepositorySnapshotStats,
		Snapshot
	} from '#lib/api/types';
	import { formatDateTime, statusLabel, triggerLabel } from '#lib/format';
	import StatusBullet from '#lib/components/StatusBullet.svelte';

	const id = $derived(page.params.id ?? '');
	const snapshotPageSize = 5;
	let repository = $state<Repository | null>(null);
	let activities = $state<RepositoryActivity[]>([]);
	let snapshots = $state<RepositorySnapshot[]>([]);
	let snapshotSummaries = $state<Record<string, Snapshot | RepositorySnapshotStats>>({});
	let nodes = $state<RepositoryNode[]>([]);
	let selectedSnapshot = $state('');
	let currentPath = $state('');
	let snapshotPage = $state(0);
	let tagFilter = $state('');
	let browserOpen = $state(false);
	let treeLoading = $state(false);
	let snapshotStatsLoading = $state(false);
	let logText = $state('');
	let error = $state('');
	let loading = $state(true);
	let busy = $state(false);
	let chosenActivityId = $state(page.url.searchParams.get('activity') ?? '');
	let source: EventSource | null = null;
	let refreshTimer: ReturnType<typeof setInterval> | undefined;
	let treeRequest = 0;
	let treeAbort: AbortController | null = null;
	let snapshotStatsRequest = 0;
	let selectedActivity = $derived(
		activities.find((a) => a.id === chosenActivityId) ??
		activities.find((a) => a.status === 'running') ?? activities[0] ?? null
	);
	let currentActivity = $derived(activities.find((a) => a.status === 'running') ?? null);
	let activeLogId = $derived(selectedActivity?.id ?? '');
	let snapshotDetails = $derived(snapshots.find((snapshot) => snapshot.id === selectedSnapshot) ?? null);
	let selectedSnapshotSummary = $derived(snapshotDetails ? snapshotSummaries[snapshotDetails.id] ?? null : null);
	let selectedSnapshotMetrics = $derived(
		selectedSnapshotSummary && (!('available' in selectedSnapshotSummary) || selectedSnapshotSummary.available)
			? selectedSnapshotSummary
			: null
	);
	// Most recent first; the tag filter is applied on the client only.
	let sortedSnapshots = $derived(
		[...snapshots].sort((a, b) => new Date(b.time).getTime() - new Date(a.time).getTime())
	);
	let availableTags = $derived([...new Set(snapshots.flatMap((snapshot) => snapshot.tags))].sort());
	let filteredSnapshots = $derived(
		tagFilter ? sortedSnapshots.filter((snapshot) => snapshot.tags.includes(tagFilter)) : sortedSnapshots
	);
	let snapshotPageCount = $derived(Math.max(1, Math.ceil(filteredSnapshots.length / snapshotPageSize)));
	let visibleSnapshots = $derived(
		filteredSnapshots.slice(snapshotPage * snapshotPageSize, (snapshotPage + 1) * snapshotPageSize)
	);

	function updateActivities(items: RepositoryActivity[]) {
		activities = items;
		if (!items.some((activity) => activity.id === chosenActivityId)) {
			chosenActivityId = items.find((activity) => activity.status === 'running')?.id ?? items[0]?.id ?? '';
		}
	}

	function formatSize(bytes: number): string {
		if (!Number.isFinite(bytes) || bytes < 0) return '—';
		const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
		let value = bytes;
		let unit = 0;
		while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit++; }
		return `${value.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
	}

	async function load() {
		loading = true;
		error = '';
		try {
			const [nextRepository, nextActivities, nextSnapshots, nextBackups] = await Promise.all([
				getRepository(id), listRepositoryActivities(id), listRepositorySnapshots(id), listBackups()
			]);
			const sameRepository = repository?.id === nextRepository.id;
			repository = nextRepository;
			updateActivities(nextActivities);
			snapshots = nextSnapshots;
			if (tagFilter && !availableTags.includes(tagFilter)) tagFilter = '';
			const summaries = sameRepository ? { ...snapshotSummaries } : {};
			for (const backup of nextBackups) {
				if (nextRepository.backups.includes(backup.name) && backup.latest_snapshot) {
					summaries[backup.latest_snapshot.id] = backup.latest_snapshot;
				}
			}
			for (const snapshot of nextSnapshots) {
				if (snapshot.summary) {
					summaries[snapshot.id] = {
						id: snapshot.id,
						timestamp: snapshot.time,
						files_added: snapshot.summary.files_new,
						files_changed: snapshot.summary.files_changed,
						bytes_added: snapshot.summary.data_added
					};
				}
			}
			snapshotSummaries = summaries;
			if (!sameRepository || !snapshots.some((snapshot) => snapshot.id === selectedSnapshot)) {
				clearSnapshotSelection();
			}
			const selectedIndex = filteredSnapshots.findIndex((snapshot) => snapshot.id === selectedSnapshot);
			if (!sameRepository) snapshotPage = 0;
			else if (selectedIndex >= 0) snapshotPage = Math.floor(selectedIndex / snapshotPageSize);
			else snapshotPage = Math.min(snapshotPage, snapshotPageCount - 1);
		} catch (reason) {
			error = reason instanceof Error ? reason.message : 'Could not load repository details.';
		} finally {
			loading = false;
		}
	}

	async function loadTree() {
		if (!selectedSnapshot || !browserOpen) {
			nodes = [];
			return;
		}
		treeAbort?.abort();
		const request = ++treeRequest;
		const snapshot = selectedSnapshot;
		const path = currentPath;
		const controller = new AbortController();
		treeAbort = controller;
		treeLoading = true;
		try {
			const result = await listSnapshotTree(id, snapshot, path, controller.signal);
			if (request === treeRequest) nodes = result;
		} catch (reason) {
			if (request === treeRequest && !controller.signal.aborted) {
				nodes = [];
				if (reason instanceof ApiError && reason.status === 404) {
					const missingSnapshot = snapshot;
					try {
						const refreshedSnapshots = await listRepositorySnapshots(id);
						if (request !== treeRequest) return;
						snapshots = refreshedSnapshots;
						snapshotPage = Math.min(
							snapshotPage,
							Math.max(0, Math.ceil(refreshedSnapshots.length / snapshotPageSize) - 1)
						);
						const stillListed = refreshedSnapshots.some((item) => item.id === missingSnapshot);
						clearSnapshotSelection();
						error = stillListed
							? 'Restic could not open this snapshot. Refresh the repository and try again.'
							: 'Snapshot is no longer available; the snapshot list was refreshed.';
					} catch (refreshError) {
						if (request === treeRequest) {
							error = refreshError instanceof Error
								? `Snapshot contents could not be loaded, and the snapshot list could not be refreshed: ${refreshError.message}`
								: 'Snapshot contents could not be loaded, and the snapshot list could not be refreshed.';
						}
					}
				} else {
					error = reason instanceof Error ? reason.message : 'Could not browse this snapshot.';
				}
			}
		} finally {
			if (request === treeRequest) {
				treeLoading = false;
				treeAbort = null;
			}
		}
	}

	function toggleSnapshotBrowser(event: Event) {
		if (!(event.currentTarget instanceof HTMLDetailsElement)) return;
		browserOpen = event.currentTarget.open;
		if (browserOpen) void loadTree();
		else cancelTreeLoad();
	}

	function cancelTreeLoad() {
		treeRequest++;
		treeAbort?.abort();
		treeAbort = null;
		treeLoading = false;
	}

	function selectSnapshot(snapshot: RepositorySnapshot) {
		selectedSnapshot = snapshot.id;
		resetSnapshotBrowser();
		snapshotStatsRequest++;
		snapshotStatsLoading = false;
		if (!snapshotSummaries[snapshot.id] && snapshot.parent) {
			void loadSnapshotStats(snapshot.id);
		}
	}

	async function loadSnapshotStats(snapshot: string) {
		const request = ++snapshotStatsRequest;
		snapshotStatsLoading = true;
		try {
			const stats = await getRepositorySnapshotStats(id, snapshot);
			if (request === snapshotStatsRequest) {
				snapshotSummaries = { ...snapshotSummaries, [snapshot]: stats };
			}
		} catch (reason) {
			if (request === snapshotStatsRequest) {
				error = reason instanceof Error ? reason.message : 'Could not load snapshot statistics.';
			}
		} finally {
			if (request === snapshotStatsRequest) snapshotStatsLoading = false;
		}
	}

	function resetSnapshotBrowser() {
		currentPath = '';
		nodes = [];
		browserOpen = false;
		cancelTreeLoad();
	}

	function changeSnapshotPage(nextPage: number) {
		snapshotPage = Math.max(0, Math.min(nextPage, snapshotPageCount - 1));
		clearSnapshotSelection();
	}

	function changeTagFilter() {
		snapshotPage = 0;
		clearSnapshotSelection();
	}

	function clearSnapshotSelection() {
		selectedSnapshot = '';
		resetSnapshotBrowser();
		snapshotStatsRequest++;
		snapshotStatsLoading = false;
	}

	async function run(kind: 'prune' | 'check') {
		busy = true;
		error = '';
		try {
			const activity = await startRepositoryActivity(id, kind);
			activities = [activity, ...activities.filter((item) => item.id !== activity.id)];
			chosenActivityId = activity.id;
		} catch (reason) {
			error = reason instanceof Error ? reason.message : `Could not start ${kind}.`;
		} finally { busy = false; }
	}

	async function unlock() {
		if (!window.confirm('Force-remove repository locks? Do this only when no external restic process is using this repository.')) return;
		busy = true;
		error = '';
		try {
			const activity = await forceRepositoryUnlock(id);
			activities = [activity, ...activities.filter((item) => item.id !== activity.id)];
			chosenActivityId = activity.id;
		} catch (reason) {
			error = reason instanceof Error ? reason.message : 'Could not unlock repository.';
		} finally { busy = false; }
	}

	$effect(() => {
		if (!id) return;
		void load();
		if (refreshTimer) clearInterval(refreshTimer);
		refreshTimer = setInterval(() => {
			void listRepositoryActivities(id).then(updateActivities).catch((reason) => {
				error = reason instanceof Error ? reason.message : 'Could not refresh repository activity.';
			});
		}, 3000);
		return () => {
			if (refreshTimer) clearInterval(refreshTimer);
			refreshTimer = undefined;
		};
	});

	$effect(() => {
		source?.close();
		source = null;
		logText = '';
		if (!activeLogId) return;
		const url = `/api/v1/repositories/${encodeURIComponent(id)}/activities/${encodeURIComponent(activeLogId)}/log/stream`;
		const stream = new EventSource(url);
		source = stream;
		stream.addEventListener('log', (event) => {
			try {
				const payload: unknown = JSON.parse((event as MessageEvent).data);
				if (typeof payload !== 'object' || payload === null || !('line' in payload) || typeof payload.line !== 'string') {
					throw new Error('Received invalid activity output.');
				}
				logText += `${payload.line}\n`;
			} catch (reason) {
				error = reason instanceof Error ? reason.message : 'Received invalid activity output.';
				stream.close();
			}
		});
		stream.addEventListener('end', () => stream.close());
		return () => {
			stream.close();
			if (source === stream) source = null;
		};
	});

	onDestroy(() => {
		source?.close();
		if (refreshTimer) clearInterval(refreshTimer);
	});
</script>

<svelte:head><title>{repository?.name ?? 'Repository'} · pgkeeper</title></svelte:head>

{#if loading}
	<p class="font-mono text-sm text-on-surface-muted">Loading repository…</p>
{:else if error && !repository}
	<div class="space-y-3"><Heading level={1} size="2xl">Repository unavailable</Heading><p class="text-sm text-danger-soft-text">{error}</p><Button onclick={() => load()}>Retry</Button></div>
{:else if repository}
	<div class="space-y-6">
		<div class="flex flex-wrap items-center justify-between gap-3">
			<div><Heading level={1} size="2xl">Repository</Heading><p class="mt-1 break-all font-mono text-xs text-on-surface-muted">{repository.name}</p>
				{#if currentActivity}<p class="mt-2 inline-flex items-center gap-2 rounded bg-primary/10 px-2 py-1 text-xs text-primary">Activity running · {currentActivity.kind}</p>{/if}
			</div>
			<div class="flex flex-wrap gap-2">
				<Button variant="secondary" disabled={busy || Boolean(currentActivity)} onclick={() => run('check')}>Run check</Button>
				<Button variant="secondary" disabled={busy || Boolean(currentActivity)} onclick={() => run('prune')}>Run prune</Button>
				<Button variant="secondary" class="text-danger-soft-text" disabled={busy || Boolean(currentActivity)} onclick={unlock}>Force unlock</Button>
				<Button variant="secondary" disabled={busy} onclick={() => load()}>Refresh</Button>
			</div>
		</div>
		{#if error}<p role="alert" class="text-sm text-danger-soft-text">{error}</p>{/if}
		<section class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
			<div class="panel"><p class="text-xs text-on-surface-muted">Stored size</p><p class="mt-2 font-mono text-xl">{formatSize(repository.total_size ?? 0)}</p></div>
			<div class="panel"><p class="text-xs text-on-surface-muted">Snapshots</p><p class="mt-2 font-mono text-xl">{repository.snapshot_count ?? 0}</p></div>
			<div class="panel"><p class="text-xs text-on-surface-muted">Next prune</p><p class="mt-2 text-sm">{repository.next_prune ? formatDateTime(repository.next_prune) : 'Not scheduled'}</p></div>
			<div class="panel"><p class="text-xs text-on-surface-muted">Next check</p><p class="mt-2 text-sm">{repository.next_check ? formatDateTime(repository.next_check) : 'Not scheduled'}</p></div>
		</section>
		{#if repository.backups.length}
			<section class="panel"><h2 class="font-semibold">Projects using this repository</h2><p class="mt-2 text-sm text-on-surface-muted">{repository.backups.join(', ')}</p></section>
		{/if}
		<section class="panel">
			<div class="flex flex-wrap items-center justify-between gap-3">
				<h2 class="font-semibold">Snapshots</h2>
				{#if availableTags.length}
					<label class="flex items-center gap-2 text-xs text-on-surface-muted">
						Tag
						<select
							class="rounded border border-outline bg-surface px-2 py-1 font-mono text-xs text-on-surface"
							aria-label="Filter snapshots by tag"
							bind:value={tagFilter}
							onchange={changeTagFilter}
						>
							<option value="">All tags</option>
							{#each availableTags as tag (tag)}
								<option value={tag}>{tag}</option>
							{/each}
						</select>
					</label>
				{/if}
			</div>
			{#if snapshots.length === 0}<p class="mt-3 text-sm text-on-surface-muted">No snapshots found.</p>{:else}
				<div class="mt-3 overflow-x-auto">
					<table class="w-full min-w-[36rem] border-collapse text-left text-sm">
						<thead class="border-b border-outline text-xs text-on-surface-muted">
							<tr>
								<th scope="col" class="px-3 py-2 font-medium">Snapshot</th>
								<th scope="col" class="px-3 py-2 font-medium">Time</th>
								<th scope="col" class="px-3 py-2 font-medium">Host</th>
								<th scope="col" class="px-3 py-2 font-medium">Tags</th>
							</tr>
						</thead>
						<tbody class="divide-y divide-outline">
							{#if visibleSnapshots.length === 0}
								<tr><td colspan="4" class="px-3 py-3 text-on-surface-muted">No snapshots with this tag.</td></tr>
							{/if}
							{#each visibleSnapshots as snapshot (snapshot.id)}
								<tr class={selectedSnapshot === snapshot.id ? 'bg-primary/10' : ''}>
									<td class="px-3 py-2">
										<button
											class="break-all text-left font-mono text-xs underline decoration-dotted underline-offset-2"
											aria-pressed={selectedSnapshot === snapshot.id}
											onclick={() => selectSnapshot(snapshot)}
										>{snapshot.short_id || snapshot.id.slice(0, 8)}</button>
									</td>
									<td class="whitespace-nowrap px-3 py-2">{formatDateTime(snapshot.time)}</td>
									<td class="px-3 py-2">{snapshot.hostname || '—'}</td>
									<td class="px-3 py-2">{snapshot.tags.length ? snapshot.tags.join(', ') : '—'}</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
				<div class="mt-3 flex flex-wrap items-center justify-between gap-2 text-xs">
					<p class="text-on-surface-muted">Page {snapshotPage + 1} of {snapshotPageCount} · {filteredSnapshots.length} snapshots</p>
					<div class="flex gap-2">
						<Button variant="secondary" disabled={snapshotPage === 0} onclick={() => changeSnapshotPage(snapshotPage - 1)}>Previous</Button>
						<Button variant="secondary" disabled={snapshotPage >= snapshotPageCount - 1} onclick={() => changeSnapshotPage(snapshotPage + 1)}>Next</Button>
					</div>
				</div>
				{#if snapshotDetails}
					<section class="mt-5 rounded border border-outline p-4" aria-label="Selected snapshot details">
						<h3 class="font-semibold">Snapshot details</h3>
						<dl class="mt-3 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
							<div class="min-w-0"><dt class="text-xs text-on-surface-muted">Snapshot ID</dt><dd class="mt-1 break-all font-mono text-xs">{snapshotDetails.id}</dd></div>
							<div><dt class="text-xs text-on-surface-muted">Files added</dt><dd class="mt-1 font-mono">{selectedSnapshotMetrics?.files_added ?? '—'}</dd></div>
							<div><dt class="text-xs text-on-surface-muted">Files changed</dt><dd class="mt-1 font-mono">{selectedSnapshotMetrics?.files_changed ?? '—'}</dd></div>
							<div><dt class="text-xs text-on-surface-muted">Bytes added</dt><dd class="mt-1 font-mono">{selectedSnapshotMetrics ? formatSize(selectedSnapshotMetrics.bytes_added) : '—'}</dd></div>
						</dl>
						{#if snapshotStatsLoading}
							<p class="mt-2 text-xs text-on-surface-muted">Loading snapshot statistics…</p>
						{:else if !selectedSnapshotMetrics && !snapshotDetails.parent}
							<p class="mt-2 text-xs text-on-surface-muted">No backup summary is recorded for this initial snapshot.</p>
						{:else if !selectedSnapshotMetrics && snapshotDetails.parent}
							<p class="mt-2 text-xs text-on-surface-muted">Snapshot statistics are unavailable.</p>
						{/if}
						<div class="mt-3 space-y-1 text-xs text-on-surface-muted">
							<p>Host: {snapshotDetails.hostname || '—'}</p>
							<p>Tags: {snapshotDetails.tags.length ? snapshotDetails.tags.join(', ') : 'None'}</p>
							<p class="break-all">Included paths: {snapshotDetails.paths.join(', ') || '—'}</p>
						</div>
						<details class="mt-4 border-t border-outline pt-3" bind:open={browserOpen} ontoggle={toggleSnapshotBrowser}>
							<summary class="cursor-pointer font-semibold">Snapshot Browser</summary>
							<div class="mt-3 flex items-center gap-2 text-xs text-on-surface-muted">
								<span class="truncate">{currentPath || '/'}</span>
								{#if currentPath}<button class="underline" onclick={() => { currentPath = currentPath.split('/').slice(0, -1).join('/'); void loadTree(); }}>Up</button>{/if}
							</div>
							{#if treeLoading}
								<div role="status" aria-live="polite" class="mt-3 flex items-center gap-2 text-sm text-on-surface-muted">
									<span aria-hidden="true" class="size-4 animate-spin rounded-full border-2 border-outline border-t-primary motion-reduce:animate-none"></span>
									<span>Loading snapshot contents…</span>
								</div>
							{:else if nodes.length === 0}
								<p class="mt-2 text-sm text-on-surface-muted">No files or folders in this location.</p>
							{:else}
								<ul class="mt-2 divide-y divide-outline">
									{#each nodes as node (node.path)}
										<li class="grid grid-cols-[minmax(0,1fr)_auto_5rem] items-center gap-3 py-2 text-sm">
											<div class="min-w-0">
												{#if node.type === 'dir'}
													<button class="truncate text-left underline" title={node.path} onclick={() => { currentPath = node.path; void loadTree(); }}>{node.name}/</button>
												{:else}
													<span class="block truncate" title={node.path}>{node.name}</span>
												{/if}
											</div>
											{#if node.type !== 'dir'}
												<a class="underline" download href={`/api/v1/repositories/${encodeURIComponent(id)}/snapshots/${encodeURIComponent(selectedSnapshot)}/download?path=${encodeURIComponent(node.path)}`}>Download</a>
												<span class="text-right font-mono text-xs tabular-nums text-on-surface-muted">{formatSize(node.size ?? 0)}</span>
											{:else}
												<span aria-hidden="true"></span>
												<span aria-hidden="true"></span>
											{/if}
										</li>
									{/each}
								</ul>
							{/if}
						</details>
					</section>
				{/if}
			{/if}
		</section>
		<section class="grid gap-6 xl:grid-cols-2">
			<section class="panel">
				<Heading level={2} size="sm">Maintenance history</Heading>
				{#if activities.length === 0}
					<p class="py-4 text-sm text-on-surface-muted">No repository activities yet.</p>
				{:else}
					<ul class="mt-3 space-y-2">
						{#each activities as activity (activity.id)}
							<li>
								<button
									type="button"
									class="w-full rounded-[4px] border px-3 py-3 text-left transition-colors"
									class:border-outline={activity.id !== selectedActivity?.id}
									class:border-primary={activity.id === selectedActivity?.id}
									class:bg-surface-sunken={activity.id === selectedActivity?.id}
									onclick={() => chosenActivityId = activity.id}
									aria-current={activity.id === selectedActivity?.id ? 'true' : undefined}
								>
									<div class="flex items-start justify-between gap-3">
										<div class="flex min-w-0 items-center gap-2">
											<StatusBullet status={activity.status} />
											<div class="min-w-0">
												<div class="flex items-center gap-2">
													<span class="font-mono text-xs text-on-surface-muted">{statusLabel(activity.status)}</span>
													{#if activity.status === 'running'}
														<span class="rounded bg-primary/10 px-1.5 py-0.5 font-mono text-[10px] text-primary">Running</span>
													{/if}
												</div>
												<p class="mt-1 truncate text-sm text-on-surface">{activity.kind} · {formatDateTime(activity.started_at)}</p>
											</div>
										</div>
										<div class="text-right text-[11px] text-on-surface-muted">{triggerLabel(activity.triggered_by)}</div>
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
			<section class="panel">
				<div class="mb-3 flex flex-wrap items-center justify-between gap-3">
					<Heading level={2} size="sm">Output</Heading>
					{#if selectedActivity}
						<div class="flex items-center gap-2 text-xs text-on-surface-muted">
							<StatusBullet status={selectedActivity.status} />
							<span>{statusLabel(selectedActivity.status)}</span>
							<span>·</span>
							<span>{formatDateTime(selectedActivity.started_at)}</span>
							<span>·</span>
							<span>{triggerLabel(selectedActivity.triggered_by)}</span>
						</div>
					{/if}
				</div>
				<pre class="mt-3 max-h-96 min-h-32 overflow-auto rounded bg-surface-sunken p-3 font-mono text-xs whitespace-pre-wrap">{logText || 'No output recorded.'}</pre>
			</section>
		</section>
	</div>
{/if}
