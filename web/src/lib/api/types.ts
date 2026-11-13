export type ActivityStatus = 'running' | 'success' | 'error' | 'warning';
export type Trigger = 'schedule' | 'manual';
export type Phase =
	| 'queued'
	| 'dump'
	| 'roles'
	| 'restic'
	| 'cleanup'
	| 'snapshot'
	| 'provision'
	| 'restore'
	| 'validate';
export type ActivityKind = 'backup' | 'test_restore' | 'restore';

export interface Activity {
	id: string;
	backup_name: string;
	database: string;
	/** Absent in older payloads; treat as 'backup'. */
	kind?: ActivityKind;
	/** 'Backup' | 'Test Restore' | 'Container Restore' */
	label?: string;
	triggered_by: Trigger;
	status: ActivityStatus;
	started_at: string;
	finished_at: string | null;
	duration_ms: number | null;
	error: string | null;
	log_url: string;
}

export interface ResticView {
	repository: string;
	tag: string;
	keep_daily?: number;
	keep_weekly?: number;
	keep_monthly?: number;
	keep_yearly?: number;
}

export interface Snapshot {
	id: string;
	timestamp: string;
	files_added: number;
	files_changed: number;
	bytes_added: number;
}

export interface Backup {
	name: string;
	cron: string;
	timezone: string;
	/** Empty when the backup uses a database-url. */
	container: string;
	/** Source URL without credentials; empty for container backups. */
	database_url?: string;
	/** Global tools-image; empty when not configured. */
	tools_image?: string;
	database: string;
	username: string;
	host: string;
	port: number;
	roles: boolean;
	restore_cron?: string;
	restore_image?: string;
	restic: ResticView;
	running: boolean;
	next_run: string | null;
	next_restore_run?: string | null;
	last_success_at: string | null;
	last_activity: Activity | null;
	latest_snapshot: Snapshot | null;
}

export interface RestoreSnapshot {
	id: string;
	short_id: string;
	timestamp: string;
	has_roles: boolean;
}

export interface ContainerRestoreRequest {
	snapshot_id: string;
	/** Mutually exclusive with database_url. */
	container?: string;
	database?: string;
	/** postgresql://… of a remote server; needs the tools image. */
	database_url?: string;
	include_roles: boolean;
}

export interface Job {
	activity_id: string;
	backup_name: string;
	database: string;
	triggered_by: Trigger;
	status: 'running';
	phase: Phase;
	started_at: string;
}

export interface Me {
	auth_enabled: boolean;
	authenticated: boolean;
	user: {
		subject?: string;
		email?: string;
		name?: string;
	} | null;
}

export interface ApiErrorBody {
	error: {
		code: string;
		message: string;
	};
}

export interface ActivityList {
	items: Activity[];
	next?: string;
}

export interface BackupList {
	items: Backup[];
}

export interface Repository {
	id: string;
	name: string;
	is_main: boolean;
	backups: string[];
	next_prune: string | null;
	next_check: string | null;
	total_size?: number;
	snapshot_count?: number;
}

export interface RepositoryActivity {
	id: string;
	kind: string;
	triggered_by: Trigger;
	status: ActivityStatus;
	started_at: string;
	finished_at: string | null;
	error: string | null;
}

export interface RepositorySnapshot {
	id: string;
	short_id: string;
	parent?: string;
	time: string;
	hostname: string;
	paths: string[];
	tags: string[];
	summary?: {
		files_new: number;
		files_changed: number;
		data_added: number;
	};
}

export interface RepositorySnapshotStats {
	available: boolean;
	files_added: number;
	files_changed: number;
	bytes_added: number;
}

export interface RepositoryNode {
	path: string;
	name: string;
	type: string;
	size?: number;
	mtime?: string;
}
