import { getActivity, listActivities, listBackupActivities } from '$lib/api/client';
import type { Activity } from '$lib/api/types';

type LoadState = 'idle' | 'loading' | 'ready' | 'error';

let recent = $state<Activity[]>([]);
let perBackup = $state<Map<string, Activity[]>>(new Map());
let state = $state<LoadState>('idle');
let error = $state<string | null>(null);

function sortRecent(list: Activity[]) {
	return [...list].sort((a, b) => Date.parse(b.started_at) - Date.parse(a.started_at));
}

export const activities = {
	get recent() {
		return recent;
	},
	get perBackup() {
		return perBackup;
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
			recent = sortRecent(await listActivities()).slice(0, 10);
			state = 'ready';
		} catch (reason) {
			error = reason instanceof Error ? reason.message : 'Could not load recent activity.';
			state = 'error';
		}
	},
	async loadFor(name: string): Promise<Activity[]> {
		state = 'loading';
		error = null;
		try {
			const items = sortRecent(await listBackupActivities(name, 10));
			perBackup = new Map(perBackup);
			perBackup.set(name, items);
			state = 'ready';
			return items;
		} catch (reason) {
			error = reason instanceof Error ? reason.message : 'Could not load project activity.';
			state = 'error';
			throw reason;
		}
	},
	upsert(activity: Activity) {
		const nextRecent = [...recent];
		const index = nextRecent.findIndex((candidate) => candidate.id === activity.id);
		if (index >= 0) nextRecent[index] = activity; else nextRecent.unshift(activity);
		recent = sortRecent(nextRecent).slice(0, 10);

		const backupList = [...(perBackup.get(activity.backup_name) ?? [])];
		const detailIndex = backupList.findIndex((candidate) => candidate.id === activity.id);
		if (detailIndex >= 0) backupList[detailIndex] = activity; else backupList.unshift(activity);
		perBackup = new Map(perBackup);
		perBackup.set(activity.backup_name, sortRecent(backupList).slice(0, 10));
	},
	byBackup(name: string): Activity[] {
		return perBackup.get(name) ?? [];
	},
	async getById(id: string): Promise<Activity> {
		const found = [...perBackup.values()].flat().find((item) => item.id === id);
		if (found) return found;
		return getActivity(id);
	},
	clear() {
		recent = [];
		perBackup = new Map();
		error = null;
		state = 'idle';
	}
};
