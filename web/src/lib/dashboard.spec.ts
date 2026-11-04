import { describe, expect, it } from 'vitest';
import type { Activity, Backup } from './api/types';
import { activityHref, deriveDashboard } from './dashboard';

function backup(name: string, finishedAt: string | null, status: Activity['status'], running = false): Backup {
	const last_activity: Activity | null = finishedAt || status === 'running'
		? {
				id: `id-${name}`,
				backup_name: name,
				database: name,
				triggered_by: 'schedule',
				status,
				started_at: '2026-01-01T00:00:00Z',
				finished_at: finishedAt,
				duration_ms: null,
				error: null,
				log_url: ''
			}
		: null;

	return {
		name,
		cron: '* * * * *',
		timezone: 'UTC',
		container: 'postgres',
		database: name,
		username: 'backup',
		host: 'localhost',
		port: 5432,
		roles: false,
		restic: { repository: 'local:/backup', tag: 'pgkeeper' },
		running,
		next_run: null,
		last_success_at: null,
		last_activity,
		latest_snapshot: null
	};
}

describe('deriveDashboard', () => {
	it('counts running projects, failed latest activities, and finds the most recent finish', () => {
		const summary = deriveDashboard([
			backup('older', '2026-01-01T00:00:00Z', 'success'),
			backup('failed', '2026-01-01T00:05:00Z', 'error'),
			backup('running', null, 'running', true),
			backup('never', null, 'success')
		]);

		expect(summary.projectCount).toBe(4);
		expect(summary.runningCount).toBe(1);
		expect(summary.failed.map((item) => item.name)).toEqual(['failed']);
		expect(summary.lastBackupAt).toBe('2026-01-01T00:05:00Z');
	});

	it('provides an empty first-run summary', () => {
		expect(deriveDashboard([])).toEqual({
			projectCount: 0,
			runningCount: 0,
			failed: [],
			lastBackupAt: null
		});
	});
});

it('encodes project and activity values into activity links', () => {
	const item = backup('db/primary', '2026-01-01T00:00:00Z', 'success').last_activity!;
	expect(activityHref(item)).toBe('/backups/db%2Fprimary?activity=id-db%2Fprimary');
});
