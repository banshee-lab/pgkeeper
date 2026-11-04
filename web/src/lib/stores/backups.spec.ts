import { describe, expect, it } from 'vitest';
import type { Backup } from '$lib/api/types';
import { backups } from './backups.svelte';

function backup(running: boolean, lastStatus: 'success' | 'error' | null): Backup {
	return {
		name: 'primary',
		cron: '* * * * *',
		timezone: 'UTC',
		container: 'postgres',
		database: 'app',
		username: 'backup',
		host: 'localhost',
		port: 5432,
		roles: false,
		restic: { repository: 'local:/backup', tag: 'pgkeeper' },
		running,
		next_run: null,
		last_success_at: null,
		last_activity: lastStatus
			? {
					id: 'activity-1',
					backup_name: 'primary',
					database: 'app',
					triggered_by: 'schedule',
					status: lastStatus,
					started_at: '2026-01-01T00:00:00Z',
					finished_at: '2026-01-01T00:01:00Z',
					duration_ms: 60_000,
					error: null,
					log_url: ''
				}
			: null,
		latest_snapshot: null
	};
}

describe('backups.statusOf', () => {
	it('prioritizes running state over the latest activity', () => {
		expect(backups.statusOf(backup(true, 'error'))).toBe('running');
	});

	it('maps latest success, error, and missing activity', () => {
		expect(backups.statusOf(backup(false, 'success'))).toBe('success');
		expect(backups.statusOf(backup(false, 'error'))).toBe('error');
		expect(backups.statusOf(backup(false, null))).toBe('never');
	});
});
