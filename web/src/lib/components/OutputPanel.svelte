<script lang="ts">
	import { Button, CodeBlock, Heading } from '@runwisp/ui';
	import type { Activity } from '#lib/api/types';
	import { formatDateTime, statusLabel, stripAnsi, triggerLabel } from '#lib/format';
	import { logStream } from '#lib/stores/log-stream.svelte';
	import StatusBullet from './StatusBullet.svelte';

	let {
		activity,
		logText,
		loading,
		error,
		onReload,
		onCopy,
		onDownload
	}: {
		activity: Activity | null;
		logText: string;
		loading: boolean;
		error: string | null;
		onReload: () => void;
		onCopy: () => void;
		onDownload: () => void;
	} = $props();
</script>

<section class="panel">
	<div class="mb-3 flex flex-wrap items-center justify-between gap-3">
		<Heading level={2} size="sm">Output</Heading>
		{#if activity}
			<div class="flex items-center gap-2 text-xs text-on-surface-muted">
				<StatusBullet status={activity.status} />
				<span>{statusLabel(activity.status)}</span>
				<span>·</span>
				<span>{formatDateTime(activity.started_at)}</span>
				<span>·</span>
				<span>{triggerLabel(activity.triggered_by)}</span>
			</div>
		{/if}
	</div>

	{#if !activity}
		<p class="py-4 text-sm text-on-surface-muted">Select an activity to view its output</p>
	{:else if loading}
		<p class="py-4 text-sm text-on-surface-muted">Loading log…</p>
	{:else if error}
		<div class="space-y-3 py-4">
			<p class="text-sm text-danger-soft-text">{error}</p>
			<Button type="button" variant="secondary" size="sm" onclick={onReload}>Retry</Button>
		</div>
	{:else}
		<div class="mb-3 flex flex-wrap gap-2">
			<Button type="button" variant="secondary" size="sm" onclick={onCopy}>Copy</Button>
			<Button type="button" variant="secondary" size="sm" onclick={onDownload}>Download</Button>
		</div>
		{#if activity.status === 'running'}
			<div class="mb-3 rounded border border-outline bg-surface-sunken px-3 py-2 text-sm text-on-surface-muted">
				{logStream.state === 'live' ? 'Live output is streaming.' : logStream.state === 'connecting' ? 'Connecting to live output…' : 'Waiting for stream to start.'}
				{#if logStream.truncated}
					<span class="ml-2 text-on-surface-faint">Older lines truncated.</span>
				{/if}
			</div>
		{/if}
		<div class="max-h-[26rem] overflow-auto rounded border border-outline bg-surface-sunken">
			<CodeBlock
				code={stripAnsi(logText || 'No output recorded.')}
				language="text"
				copyable={true}
				variant="surface"
				size="sm"
			/>
		</div>
	{/if}
</section>
