import { describe, expect, it, vi } from 'vitest';
import type { Activity, Repository, RepositoryActivity } from '#lib/api/types';

const mocks = vi.hoisted(() => ({
	getActivity: vi.fn(),
	listActivities: vi.fn(),
	listBackupActivities: vi.fn(),
	listRepositories: vi.fn(),
	listRepositoryActivities: vi.fn()
}));

vi.mock('#lib/api/client', () => mocks);

import { activities } from './activities.svelte';

describe('activities.load', () => {
	it('merges repository and backup activity in newest-first order', async () => {
		const backupActivity: Activity = {
			id: 'backup-activity',
			backup_name: 'billing',
			database: 'billing',
			triggered_by: 'schedule',
			status: 'success',
			started_at: '2026-01-02T00:00:00Z',
			finished_at: '2026-01-02T00:01:00Z',
			duration_ms: 60_000,
			error: null,
			log_url: ''
		};
		const repositoryActivity: RepositoryActivity = {
			id: 'repository-activity',
			kind: 'prune',
			triggered_by: 'schedule',
			status: 'success',
			started_at: '2026-01-03T00:00:00Z',
			finished_at: '2026-01-03T00:01:00Z',
			error: null
		};
		const repository = {
			id: 'repo-id',
			name: 'primary',
			is_main: true,
			backups: [],
			next_prune: null,
			next_check: null
		} satisfies Repository;

		mocks.listActivities.mockResolvedValue([backupActivity]);
		mocks.listRepositories.mockResolvedValue([repository]);
		mocks.listRepositoryActivities.mockResolvedValue([repositoryActivity]);

		try {
			await activities.load();

			expect(activities.recent).toEqual([
				{
					type: 'repository',
					activity: repositoryActivity,
					repository: { id: 'repo-id', name: 'primary', is_main: true, backups: [] }
				},
				{ type: 'backup', activity: backupActivity }
			]);
			expect(mocks.listRepositoryActivities).toHaveBeenCalledWith('repo-id');
		} finally {
			activities.clear();
		}
	});
});
