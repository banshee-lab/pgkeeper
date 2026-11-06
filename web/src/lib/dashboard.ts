import type { Activity, Backup, Repository } from './api/types';

export interface UpcomingSchedule {
	key: string;
	label: string;
	detail: string;
	scheduledAt: string;
	href: string;
}

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

export function repositoryActivityHref(repositoryId: string, activityId: string): string {
	return `/repositories/${encodeURIComponent(repositoryId)}?activity=${encodeURIComponent(activityId)}`;
}

export function repositoryLabel(repository: Pick<Repository, 'name' | 'is_main' | 'backups'>): string {
	if (repository.is_main) return 'Main Repository';
	const firstBackup = repository.backups[0];
	return firstBackup ? `${firstBackup} Repository` : repository.name;
}

export function deriveSchedules(backups: Backup[], repositories: Repository[]): UpcomingSchedule[] {
	const schedules: UpcomingSchedule[] = [];

	for (const backup of backups) {
		if (backup.next_run) {
			schedules.push({
				key: `backup-${backup.name}`,
				label: backup.name,
				detail: 'Backup',
				scheduledAt: backup.next_run,
				href: projectHref(backup.name)
			});
		}
	}

	for (const repository of repositories) {
		for (const [kind, scheduledAt] of [
			['Prune', repository.next_prune],
			['Check', repository.next_check]
		] as const) {
			if (scheduledAt) {
				schedules.push({
					key: `repository-${repository.id}-${kind.toLowerCase()}`,
					label: repositoryLabel(repository),
					detail: kind,
					scheduledAt,
					href: `/repositories/${encodeURIComponent(repository.id)}`
				});
			}
		}
	}

	return schedules.sort((a, b) => Date.parse(a.scheduledAt) - Date.parse(b.scheduledAt));
}

export function projectHref(name: string): string {
	return `/backups/${encodeURIComponent(name)}`;
}
