import { getBackup, listBackups } from '#lib/api/client';
import type { Backup } from '#lib/api/types';

type LoadState = 'idle' | 'loading' | 'ready' | 'error';

let items = $state<Backup[]>([]);
let state = $state<LoadState>('idle');
let error = $state<string | null>(null);

function sortBackups(values: Backup[]): Backup[] {
	return [...values].sort((a, b) => a.name.localeCompare(b.name));
}

function mergeBackup(backup: Backup): void {
	const index = items.findIndex((candidate) => candidate.name === backup.name);
	if (index >= 0) {
		items[index] = backup;
	} else {
		items.push(backup);
	}
	items = sortBackups(items);
}

export const backups = {
	get list() {
		return items;
	},
	get state() {
		return state;
	},
	get error() {
		return error;
	},
	async load() {
		state = 'loading';
		error = null;
		try {
			items = sortBackups(await listBackups());
			state = 'ready';
		} catch (reason) {
			error = reason instanceof Error ? reason.message : 'Could not load projects.';
			state = 'error';
		}
	},
	async loadOne(name: string): Promise<Backup> {
		state = 'loading';
		error = null;
		try {
			const backup = await getBackup(name);
			mergeBackup(backup);
			state = 'ready';
			return backup;
		} catch (reason) {
			error = reason instanceof Error ? reason.message : 'Could not load project.';
			state = 'error';
			throw reason;
		}
	},
	clear() {
		items = [];
		error = null;
		state = 'idle';
	},
	statusOf(backup: Backup): 'running' | 'success' | 'error' | 'warning' | 'never' {
		if (backup.running) return 'running';
		if (backup.last_activity?.status === 'success') return 'success';
		if (backup.last_activity?.status === 'error') return 'error';
		if (backup.last_activity?.status === 'warning') return 'warning';
		return 'never';
	}
};
