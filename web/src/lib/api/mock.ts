import type { Activity, Backup, Me, Snapshot } from './types';

const now = Date.now();
const at = (offsetMs: number) => new Date(now - offsetMs).toISOString();

const activityLogs: Record<string, string> = {
	'f04b8be2-26a6-42b8-a19d-4c566fa57001': `2026-01-01T00:00:00Z pg_dump: Dumping database schema\n2026-01-01T00:00:01Z pg_dump: Dumping data\n2026-01-01T00:00:03Z pg_dump: Done.`,
	'f04b8be2-26a6-42b8-a19d-4c566fa57002': `\u001b[31m2026-01-01T00:05:00Z ERROR: restic repository is locked by another process\u001b[0m\n2026-01-01T00:05:02Z cleanup: removing temp files`,
	'f04b8be2-26a6-42b8-a19d-4c566fa57003': `2026-01-01T00:00:00Z pg_dump: dump complete\n2026-01-01T00:00:02Z restic: snapshot created`,
	'f04b8be2-26a6-42b8-a19d-4c566fa57004': `2026-01-01T00:00:00Z pg_dump: creating dump\n2026-01-01T00:00:01Z pg_dump: copy complete\n2026-01-01T00:00:03Z restic: uploading snapshot`,
	'f04b8be2-26a6-42b8-a19d-4c566fa57005': `2026-01-01T00:00:00Z pg_dump: schema ok\n2026-01-01T00:00:01Z restic: done`,
	'f04b8be2-26a6-42b8-a19d-4c566fa57006': `2026-01-01T00:00:00Z pg_dump: dump ok`,
	'f04b8be2-26a6-42b8-a19d-4c566fa57007': `2026-01-01T00:00:00Z pg_dump: backup complete`,
	'f04b8be2-26a6-42b8-a19d-4c566fa57008': `2026-01-01T00:00:00Z pg_dump: uploaded`,
	'f04b8be2-26a6-42b8-a19d-4c566fa57009': `2026-01-01T00:00:00Z pg_dump: success`,
	'f04b8be2-26a6-42b8-a19d-4c566fa57010': `2026-01-01T00:00:00Z pg_dump: success`
};

const activities: Activity[] = [
	{
		id: 'f04b8be2-26a6-42b8-a19d-4c566fa57001',
		backup_name: 'billing-prod',
		database: 'billing',
		triggered_by: 'schedule',
		status: 'success',
		started_at: at(1_200_000),
		finished_at: at(1_110_000),
		duration_ms: 90_000,
		error: null,
		log_url: ''
	},
	{
		id: 'f04b8be2-26a6-42b8-a19d-4c566fa57002',
		backup_name: 'analytics',
		database: 'warehouse',
		triggered_by: 'schedule',
		status: 'error',
		started_at: at(3_900_000),
		finished_at: at(3_810_000),
		duration_ms: 90_000,
		error: 'restic: repository is locked by another process',
		log_url: ''
	},
	{
		id: 'f04b8be2-26a6-42b8-a19d-4c566fa57003',
		backup_name: 'customers',
		database: 'customers',
		triggered_by: 'manual',
		status: 'success',
		started_at: at(8_000_000),
		finished_at: at(7_890_000),
		duration_ms: 110_000,
		error: null,
		log_url: ''
	},
	{
		id: 'f04b8be2-26a6-42b8-a19d-4c566fa57004',
		backup_name: 'inventory',
		database: 'inventory',
		triggered_by: 'schedule',
		status: 'running',
		started_at: at(42_000),
		finished_at: null,
		duration_ms: null,
		error: null,
		log_url: ''
	},
	{
		id: 'f04b8be2-26a6-42b8-a19d-4c566fa57005',
		backup_name: 'auth',
		database: 'auth',
		triggered_by: 'schedule',
		status: 'success',
		started_at: at(89_000_000),
		finished_at: at(88_900_000),
		duration_ms: 100_000,
		error: null,
		log_url: ''
	},
	{
		id: 'f04b8be2-26a6-42b8-a19d-4c566fa57006',
		backup_name: 'reporting',
		database: 'reports',
		triggered_by: 'schedule',
		status: 'success',
		started_at: at(176_000_000),
		finished_at: at(175_910_000),
		duration_ms: 90_000,
		error: null,
		log_url: ''
	},
	{
		id: 'f04b8be2-26a6-42b8-a19d-4c566fa57007',
		backup_name: 'staging',
		database: 'app',
		triggered_by: 'manual',
		status: 'success',
		started_at: at(265_000_000),
		finished_at: at(264_940_000),
		duration_ms: 60_000,
		error: null,
		log_url: ''
	},
	{
		id: 'f04b8be2-26a6-42b8-a19d-4c566fa57008',
		backup_name: 'events',
		database: 'events',
		triggered_by: 'schedule',
		status: 'success',
		started_at: at(350_000_000),
		finished_at: at(349_850_000),
		duration_ms: 150_000,
		error: null,
		log_url: ''
	},
	{
		id: 'f04b8be2-26a6-42b8-a19d-4c566fa57009',
		backup_name: 'ledger',
		database: 'ledger',
		triggered_by: 'schedule',
		status: 'success',
		started_at: at(440_000_000),
		finished_at: at(439_900_000),
		duration_ms: 100_000,
		error: null,
		log_url: ''
	},
	{
		id: 'f04b8be2-26a6-42b8-a19d-4c566fa57010',
		backup_name: 'search',
		database: 'search',
		triggered_by: 'manual',
		status: 'success',
		started_at: at(530_000_000),
		finished_at: at(529_910_000),
		duration_ms: 90_000,
		error: null,
		log_url: ''
	}
];

const snapshotFor = (activity: Activity | null, index: number): Snapshot | null =>
	activity?.status === 'success' && activity.finished_at
		? {
				id: `${(0xa1b2c3d4 + index * 0x1111).toString(16)}e5f6a7b8c9d0`,
				timestamp: activity.started_at,
				files_added: 2 + index,
				files_changed: index % 3,
				bytes_added: (index + 1) * 7_340_032
			}
		: null;

const defaultBackup = (name: string, lastActivity: Activity | null, running = false, index = 0): Backup => ({
	name,
	cron: '0 */6 * * *',
	timezone: 'UTC',
	container: 'postgres-primary',
	database: lastActivity?.database ?? name,
	username: 'backup',
	host: 'postgres.internal',
	port: 5432,
	roles: true,
	restic: {
		repository: 's3:https://backup.example.net/pgkeeper',
		tag: 'pgkeeper',
		keep_daily: 7,
		keep_weekly: 4,
		keep_monthly: 12,
		keep_yearly: 2
	},
	running,
	next_run: at(-3_600_000),
	last_success_at: lastActivity?.status === 'success' ? lastActivity.finished_at : null,
	last_activity: lastActivity,
	latest_snapshot: snapshotFor(lastActivity, index)
});

export const mockMe: Me = {
	auth_enabled: false,
	authenticated: true,
	user: null
};

export const mockBackups: Backup[] = [
	defaultBackup('billing-prod', activities[0], false, 0),
	defaultBackup('analytics', activities[1], false, 1),
	defaultBackup('customers', activities[2], false, 2),
	defaultBackup('inventory', activities[3], true, 3),
	defaultBackup('auth', activities[4], false, 4),
	defaultBackup('reporting', activities[5], false, 5),
	defaultBackup('staging', activities[6], false, 6),
	defaultBackup('events', activities[7], false, 7),
	defaultBackup('ledger', activities[8], false, 8),
	defaultBackup('search', activities[9], false, 9),
	defaultBackup('new-service', null)
];

export const mockActivities = activities;

export function copyMockBackups(): Backup[] {
	return structuredClone(mockBackups);
}

export function copyMockActivities(): Activity[] {
	return structuredClone(mockActivities);
}

export function copyMockMe(): Me {
	return structuredClone(mockMe);
}

export function getMockLog(activityId: string): string {
	return activityLogs[activityId] ?? 'No output recorded.';
}
