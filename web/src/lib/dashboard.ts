import type { Activity, Backup } from './api/types';

export interface DashboardSummary {
	projectCount: number;
	runningCount: number;
	failed: Backup[];
	lastBackupAt: string | null;
}

export function deriveDashboard(backups: Backup[]): DashboardSummary {
	const failures = backups.filter((backup) => backup.last_activity?.status === 'error');
	const finished = backups
		.map((backup) => backup.last_activity?.finished_at ?? null)
		.filter((value): value is string => value !== null);
	const lastBackupAt = finished.reduce<string | null>(
		(latest, value) =>
			latest === null || Date.parse(value) > Date.parse(latest) ? value : latest,
		null
	);

	return {
		projectCount: backups.length,
		runningCount: backups.filter((backup) => backup.running).length,
		failed: failures,
		lastBackupAt
	};
}

export function activityHref(activity: Activity): string {
	return `/backups/${encodeURIComponent(activity.backup_name)}?activity=${encodeURIComponent(activity.id)}`;
}

export function projectHref(name: string): string {
	return `/backups/${encodeURIComponent(name)}`;
}
