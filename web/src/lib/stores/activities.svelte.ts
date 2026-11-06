import {
	getActivity,
	listActivities,
	listBackupActivities,
	listRepositories,
	listRepositoryActivities
} from '#lib/api/client';
import type { Activity, Repository, RepositoryActivity } from '#lib/api/types';

type LoadState = 'idle' | 'loading' | 'ready' | 'error';
export type RecentItem =
	| { type: 'backup'; activity: Activity }
	| {
			type: 'repository';
			activity: RepositoryActivity;
			repository: Pick<Repository, 'id' | 'name' | 'is_main' | 'backups'>;
	  };

let recent = $state<RecentItem[]>([]);
let repositoryItems = $state<Repository[]>([]);
let perBackup = $state<Map<string, Activity[]>>(new Map());
let state = $state<LoadState>('idle');
let error = $state<string | null>(null);

function sortByStartedAt<T extends { started_at: string }>(list: T[]): T[] {
	return [...list].sort((a, b) => Date.parse(b.started_at) - Date.parse(a.started_at));
}

function sortRecent(list: RecentItem[]) {
	return [...list].sort(
		(a, b) => Date.parse(b.activity.started_at) - Date.parse(a.activity.started_at)
	);
}

export const activities = {
	get recent() {
		return recent;
	},
	get repositories() {
		return repositoryItems;
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
			const [backupActivities, repositories] = await Promise.all([listActivities(), listRepositories()]);
			repositoryItems = repositories;
			const repositoryActivities = await Promise.all(
				repositories.map(async (repository) => ({
					repository: {
						id: repository.id,
						name: repository.name,
						is_main: repository.is_main,
						backups: repository.backups
					},
					activities: await listRepositoryActivities(repository.id)
				}))
			);
			recent = sortRecent([
				...backupActivities.map((activity): RecentItem => ({ type: 'backup', activity })),
				...repositoryActivities.flatMap(({ repository, activities }) =>
					activities.map(
						(activity): RecentItem => ({ type: 'repository', activity, repository })
					)
				)
			]).slice(0, 10);
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
			const items = sortByStartedAt(await listBackupActivities(name, 10));
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
		const item: RecentItem = { type: 'backup', activity };
		const nextRecent = [...recent];
		const index = nextRecent.findIndex(
			(candidate) => candidate.type === 'backup' && candidate.activity.id === activity.id
		);
		if (index >= 0) nextRecent[index] = item; else nextRecent.unshift(item);
		recent = sortRecent(nextRecent).slice(0, 10);

		const backupList = [...(perBackup.get(activity.backup_name) ?? [])];
		const detailIndex = backupList.findIndex((candidate) => candidate.id === activity.id);
		if (detailIndex >= 0) backupList[detailIndex] = activity; else backupList.unshift(activity);
		perBackup = new Map(perBackup);
		perBackup.set(activity.backup_name, sortByStartedAt(backupList).slice(0, 10));
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
		repositoryItems = [];
		perBackup = new Map();
		error = null;
		state = 'idle';
	}
};
