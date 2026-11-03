<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { Button, Heading } from '@runwisp/ui';
	import { ApiError, getActivity, getActivityLog } from '$lib/api/client';
	import ActivityList from '$lib/components/ActivityList.svelte';
	import DetailsPanel from '$lib/components/DetailsPanel.svelte';
	import OutputPanel from '$lib/components/OutputPanel.svelte';
	import RunButton from '$lib/components/RunButton.svelte';
	import { stripAnsi } from '$lib/format';
	import type { Activity } from '$lib/api/types';
	import { activities } from '$lib/stores/activities.svelte';
	import { backups } from '$lib/stores/backups.svelte';
	import { logStream } from '$lib/stores/log-stream.svelte';

	const name = $derived(page.params.name ?? '');
	const backup = $derived(backups.list.find((candidate) => candidate.name === name) ?? null);
	const list = $derived(activities.byBackup(name).slice(0, 10));
	const selectedActivity = $derived(list.find((item) => item.id === selectedId) ?? null);

	let loading = $state(true);
	let error = $state<string | null>(null);
	let selectedId = $state<string | null>(null);
	let logText = $state('');
	let logLoading = $state(false);
	let logError = $state<string | null>(null);
	let activeLogRequest = 0;
	const liveLogText = $derived(
		selectedActivity && selectedActivity.status === 'running' ? logStream.lines.join('\n') : logText
	);

	function updateSelection(nextId: string | null) {
		selectedId = nextId;
		if (!nextId) {
			const next = new URL(page.url);
			next.searchParams.delete('activity');
			if (next.search !== page.url.search) {
				void goto(next, { replaceState: true, keepFocus: true, noScroll: true });
			}
			return;
		}
		const next = new URL(page.url);
		next.searchParams.set('activity', nextId);
		if (next.search !== page.url.search) {
			void goto(next, { replaceState: true, keepFocus: true, noScroll: true });
		}
	}

	async function loadProject() {
		if (!name) return;
		loading = true;
		error = null;
		try {
			await Promise.all([backups.loadOne(name), activities.loadFor(name)]);
			const currentList = activities.byBackup(name);
			const queryId = page.url.searchParams.get('activity');
			let nextSelection: string | null = selectedId;
			if (queryId) {
				const matching = currentList.find((item) => item.id === queryId);
				if (matching) {
					nextSelection = matching.id;
				} else {
					try {
						const activity = await getActivity(queryId);
						if (activity.backup_name === name) {
							activities.upsert(activity);
							nextSelection = activity.id;
						} else {
							nextSelection = currentList[0]?.id ?? null;
						}
					} catch (reason) {
						if (reason instanceof ApiError && reason.status === 404) {
							const next = new URL(page.url);
							next.searchParams.delete('activity');
							await goto(next, { replaceState: true, keepFocus: true, noScroll: true });
							nextSelection = currentList[0]?.id ?? null;
						} else {
							throw reason;
						}
					}
				}
			} else {
				nextSelection = currentList.find((item) => item.status === 'running')?.id ?? currentList[0]?.id ?? null;
			}
			updateSelection(nextSelection);
		} catch (reason) {
			error = reason instanceof Error ? reason.message : 'Could not load project.';
		} finally {
			loading = false;
		}
	}

	async function loadLog(activity: Activity) {
		if (activity.status === 'running') {
			logLoading = false;
			logError = null;
			logText = '';
			await logStream.create(activity.id);
			return;
		}
		logStream.close();
		const token = ++activeLogRequest;
		logLoading = true;
		logError = null;
		try {
			const log = await getActivityLog(activity.id);
			if (token !== activeLogRequest) return;
			logText = stripAnsi(log || '');
		} catch (reason) {
			if (token !== activeLogRequest) return;
			logError = reason instanceof Error ? reason.message : 'Could not load log.';
			logText = '';
		} finally {
			if (token === activeLogRequest) {
				logLoading = false;
			}
		}
	}

	async function handleCopy() {
		if (!selectedActivity) return;
		const textToCopy = selectedActivity.status === 'running' ? liveLogText : logText;
		if (!textToCopy) return;
		await navigator.clipboard.writeText(textToCopy);
	}

	function handleDownload() {
		if (!selectedActivity) return;
		const textToDownload = selectedActivity.status === 'running' ? liveLogText : logText;
		const blob = new Blob([textToDownload], { type: 'text/plain;charset=utf-8' });
		const url = URL.createObjectURL(blob);
		const link = document.createElement('a');
		link.href = url;
		link.download = `${selectedActivity.backup_name}-${selectedActivity.started_at}.log`;
		link.click();
		URL.revokeObjectURL(url);
	}

	$effect(() => {
		if (!name) return;
		void loadProject();
	});

	$effect(() => {
		if (!selectedActivity) {
			logStream.close();
			logText = '';
			logLoading = false;
			logError = null;
			return;
		}
		void loadLog(selectedActivity);
	});
</script>

<svelte:head>
	<title>{name || 'Project'} · pgkeeper</title>
</svelte:head>

{#if loading}
	<div class="space-y-4">
		<p class="font-mono text-xs uppercase tracking-[0.2em] text-on-surface-faint">Loading project…</p>
		<div class="h-10 animate-pulse rounded border border-outline bg-surface-sunken"></div>
	</div>
{:else if !backup}
	<div class="space-y-4">
		<Heading level={1} size="2xl">Project not found</Heading>
		<p class="text-sm text-on-surface-muted">The backup <span class="font-mono">{name}</span> could not be found.</p>
		<Button type="button" variant="secondary" href="/">Back to dashboard</Button>
	</div>
{:else if error}
	<div class="space-y-4">
		<Heading level={1} size="2xl">Project unavailable</Heading>
		<p class="text-sm text-danger-soft-text">{error}</p>
		<Button type="button" variant="secondary" onclick={() => void loadProject()}>Retry</Button>
	</div>
{:else}
	<div class="space-y-6">
		<div class="flex flex-wrap items-center justify-between gap-3">
			<Heading level={1} size="2xl">{backup.name}</Heading>
			<RunButton name={backup.name} running={Boolean(backup.running)} />
		</div>

		<DetailsPanel {backup} />

		<div class="grid gap-6 xl:grid-cols-[minmax(0,24rem)_minmax(0,1fr)]">
			<ActivityList items={list} selectedId={selectedId} onSelect={updateSelection} />
			<OutputPanel
				activity={selectedActivity}
				logText={selectedActivity && selectedActivity.status === 'running' ? liveLogText : logText}
				loading={logLoading}
				error={logError}
				onReload={() => selectedActivity && void loadLog(selectedActivity)}
				onCopy={handleCopy}
				onDownload={handleDownload}
			/>
		</div>
	</div>
{/if}
