import type { Job } from '$lib/api/types';

let byActivity = $state<Map<string, Job>>(new Map());

export const jobs = {
	get list() {
		return [...byActivity.values()];
	},
	get runningCount() {
		return byActivity.size;
	},
	byBackup(name: string): Job | undefined {
		return [...byActivity.values()].find((job) => job.backup_name === name);
	},
	applySnapshot(items: Job[]) {
		byActivity = new Map(items.map((job) => [job.activity_id, job]));
	},
	clear() {
		byActivity = new Map();
	}
};
