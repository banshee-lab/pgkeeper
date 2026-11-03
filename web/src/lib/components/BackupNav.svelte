<script lang="ts">
	import { Input } from '@runwisp/ui';
	import { page } from '$app/state';
	import type { Backup } from '$lib/api/types';
	import { projectHref } from '$lib/dashboard';
	import { backups } from '$lib/stores/backups.svelte';
	import StatusBullet from './StatusBullet.svelte';

	interface Props {
		onNavigate: () => void;
	}

	let { onNavigate }: Props = $props();
	let filter = $state('');
	const filtered = $derived(
		backups.list.filter((backup) => backup.name.toLowerCase().includes(filter.trim().toLowerCase()))
	);

	function statusOf(backup: Backup) {
		return backups.statusOf(backup);
	}
</script>

<nav aria-label="Primary navigation" class="flex min-h-0 flex-1 flex-col gap-3 px-3 py-4">
	<a
		href="/"
		onclick={onNavigate}
		aria-current={page.url.pathname === '/' ? 'page' : undefined}
		class="rounded-[3px] px-3 py-2 font-mono text-sm text-on-surface-muted hover:bg-surface-sunken hover:text-on-surface aria-[current=page]:bg-surface-sunken aria-[current=page]:text-on-surface"
	>
		Dashboard
	</a>

	<div class="px-1">
		<Input bind:value={filter} placeholder="Filter projects…" aria-label="Filter projects" size="sm" />
	</div>

	<div class="min-h-0 flex-1 overflow-y-auto">
		<p class="px-3 pb-2 pt-1 font-mono text-[0.65rem] font-semibold uppercase tracking-wider text-on-surface-faint">
			Projects <span class="font-normal">({filtered.length})</span>
		</p>

		{#if backups.state === 'loading' && backups.list.length === 0}
			<p class="px-3 py-2 text-xs text-on-surface-muted">Loading projects…</p>
		{:else if backups.state === 'error' && backups.list.length === 0}
			<p class="px-3 py-2 text-xs text-danger-soft-text">{backups.error}</p>
		{:else if filtered.length === 0}
			<p class="px-3 py-2 text-xs text-on-surface-muted">
				{filter ? 'No matching projects.' : 'No projects configured.'}
			</p>
		{:else}
			<ul class="space-y-0.5">
				{#each filtered as backup (backup.name)}
					<li>
						<a
							href={projectHref(backup.name)}
							onclick={onNavigate}
							aria-current={page.url.pathname === projectHref(backup.name) ? 'page' : undefined}
							class="flex items-center gap-2 rounded-[3px] px-3 py-2 font-mono text-xs text-on-surface-muted hover:bg-surface-sunken hover:text-on-surface aria-[current=page]:bg-surface-sunken aria-[current=page]:text-on-surface"
						>
							<StatusBullet status={statusOf(backup)} />
							<span class="min-w-0 truncate">{backup.name}</span>
						</a>
					</li>
				{/each}
			</ul>
		{/if}
	</div>
</nav>
