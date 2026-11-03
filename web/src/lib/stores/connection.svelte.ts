import { jobs } from '$lib/stores/jobs.svelte';
import { activities } from '$lib/stores/activities.svelte';
import { backups } from '$lib/stores/backups.svelte';
import { session } from '$lib/stores/session.svelte';
import type { Activity, Job } from '$lib/api/types';

export type ConnectionState = 'connecting' | 'connected' | 'disconnected';
export type JobsEventType = 'snapshot' | 'job.started' | 'job.phase' | 'job.finished';

let state = $state<ConnectionState>('connecting');
let source: EventSource | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let reconnectDelayMs = 1_000;
const subscribers = new Map<JobsEventType, Set<(payload: unknown) => void>>();

function notify<T>(type: JobsEventType, payload: T) {
	for (const handler of subscribers.get(type) ?? []) handler(payload);
}

function safeParse<T>(data: string): T | null {
	try {
		return JSON.parse(data) as T;
	} catch {
		return null;
	}
}

function applySnapshot(items: Job[]) {
	jobs.applySnapshot(items);
	void backups.load();
	void activities.load();
}

function applyStarted(job: Job) {
	jobs.started(job);
	const backup = backups.list.find((item) => item.name === job.backup_name);
	if (backup) {
		backup.running = true;
		backup.last_activity = {
			id: job.activity_id,
			backup_name: job.backup_name,
			database: job.database,
			triggered_by: job.triggered_by,
			status: 'running',
			started_at: job.started_at,
			finished_at: null,
			duration_ms: null,
			error: null,
			log_url: `/api/v1/activities/${job.activity_id}/log`
		};
	}
	activities.upsert({
		id: job.activity_id,
		backup_name: job.backup_name,
		database: job.database,
		triggered_by: job.triggered_by,
		status: 'running',
		started_at: job.started_at,
		finished_at: null,
		duration_ms: null,
		error: null,
		log_url: `/api/v1/activities/${job.activity_id}/log`
	});
	notify('job.started', job);
}

function applyPhase(job: Job) {
	jobs.phase(job.activity_id, job.phase);
	notify('job.phase', job);
}

function applyFinished(payload: { job: Job; activity: Activity }) {
	jobs.finished(payload.job.activity_id);
	const target = backups.list.find((item) => item.name === payload.activity.backup_name);
	if (target) {
		target.running = false;
		target.last_activity = payload.activity;
	}
	activities.upsert(payload.activity);
	notify('job.finished', payload);
}

function scheduleReconnect() {
	if (reconnectTimer || typeof window === 'undefined') return;
	const timeout = Math.min(reconnectDelayMs, 30_000);
	reconnectDelayMs = Math.min(reconnectDelayMs * 2, 30_000);
	state = 'disconnected';
	reconnectTimer = window.setTimeout(() => {
		reconnectTimer = null;
		void (async () => {
			try {
				const response = await fetch('/api/v1/me', { credentials: 'same-origin' });
				if (response.status === 401) {
					session.expire();
					return;
				}
				if (response.ok) {
					openJobsStream();
				}
			} catch {
				openJobsStream();
			}
		})();
	}, timeout);
}

function bindSource(sourceInstance: EventSource) {
	sourceInstance.onopen = () => {
		reconnectDelayMs = 1_000;
		state = 'connected';
	};
	sourceInstance.onerror = () => {
		state = 'disconnected';
		if (sourceInstance.readyState === EventSource.CLOSED) {
			source = null;
			scheduleReconnect();
			return;
		}
	};
	const handlers: Record<JobsEventType, (event: MessageEvent) => void> = {
		snapshot: (event) => {
			const payload = safeParse<{ items: Job[] }>(event.data);
			if (!payload?.items) return;
			applySnapshot(payload.items);
			notify('snapshot', payload.items);
		},
		'job.started': (event) => {
			const payload = safeParse<Job>(event.data);
			if (!payload) return;
			applyStarted(payload);
		},
		'job.phase': (event) => {
			const payload = safeParse<Job>(event.data);
			if (!payload) return;
			applyPhase(payload);
		},
		'job.finished': (event) => {
			const payload = safeParse<{ job: Job; activity: Activity }>(event.data);
			if (!payload) return;
			applyFinished(payload);
		}
	};

	for (const eventType of Object.keys(handlers) as JobsEventType[]) {
		sourceInstance.addEventListener(eventType, handlers[eventType]);
	}
}

function openJobsStream() {
	if (typeof EventSource === 'undefined') return;
	if (source) return;
	if (reconnectTimer) {
		clearTimeout(reconnectTimer);
		reconnectTimer = null;
	}
	state = 'connecting';
	source = new EventSource('/api/v1/jobs/stream');
	bindSource(source);
}

export const connection = {
	get state() {
		return state;
	},
	setState(next: ConnectionState) {
		state = next;
	},
	subscribe(type: JobsEventType, handler: (payload: unknown) => void) {
		const bucket = subscribers.get(type) ?? new Set();
		bucket.add(handler);
		subscribers.set(type, bucket);
		return () => {
			bucket.delete(handler);
			if (bucket.size === 0) subscribers.delete(type);
		};
	},
	connect() {
		openJobsStream();
	},
	close() {
		if (reconnectTimer) {
			clearTimeout(reconnectTimer);
			reconnectTimer = null;
		}
		if (!source) {
			state = 'disconnected';
			return;
		}
		source.close();
		source = null;
		state = 'disconnected';
	}
};
