import type { Activity, Job } from '$lib/api/types';

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
	started(job: Job) {
		byActivity = new Map(byActivity);
		byActivity.set(job.activity_id, job);
	},
	phase(activityId: string, phase: Job['phase']) {
		const current = byActivity.get(activityId);
		if (!current) return;
		byActivity = new Map(byActivity);
		byActivity.set(activityId, { ...current, phase });
	},
	finished(activityId: string) {
		if (!byActivity.has(activityId)) return;
		byActivity = new Map(byActivity);
		byActivity.delete(activityId);
	},
	clear() {
		byActivity = new Map();
	}
};
