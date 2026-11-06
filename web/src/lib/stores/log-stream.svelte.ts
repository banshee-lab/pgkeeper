import { getActivityLog } from '#lib/api/client';
import { stripAnsi } from '#lib/format';

export type LogStreamState = 'idle' | 'connecting' | 'live' | 'ended' | 'failed';

let activityId = $state<string | null>(null);
let state = $state<LogStreamState>('idle');
let lines = $state<string[]>([]);
let error = $state<string | null>(null);
let source: EventSource | null = null;
let seenEventIds = new Set<number>();
let truncated = $state(false);

function safeParse<T>(value: string): T | null {
	try {
		return JSON.parse(value) as T;
	} catch {
		return null;
	}
}

function applyBuffered(next: string[]) {
	const buffer = next.length > 20_000 ? next.slice(next.length - 20_000) : next;
	truncated = next.length > buffer.length;
	lines = buffer;
}

function appendLine(line: string) {
	const next = [...lines, stripAnsi(line)];
	applyBuffered(next);
}

export const logStream = {
	get activityId() {
		return activityId;
	},
	get state() {
		return state;
	},
	get lines() {
		return lines;
	},
	get error() {
		return error;
	},
	get truncated() {
		return truncated;
	},
	async create(nextActivityId: string) {
		if (source && activityId === nextActivityId) return;
		this.close();
		activityId = nextActivityId;
		state = 'connecting';
		error = null;
		truncated = false;
		seenEventIds.clear();
		lines = [];

		if (typeof EventSource === 'undefined') {
			state = 'failed';
			return;
		}

		source = new EventSource(`/api/v1/activities/${encodeURIComponent(nextActivityId)}/log/stream`);
		source.addEventListener('log', (event) => {
			const payload = safeParse<{ id?: number | string; line?: string }>(event.data);
			if (!payload?.line) return;
			const id = Number(payload.id ?? event.lastEventId);
			if (!Number.isNaN(id) && seenEventIds.has(id)) return;
			if (!Number.isNaN(id)) seenEventIds.add(id);
			state = 'live';
			appendLine(payload.line);
		});
		source.addEventListener('end', (event) => {
			const payload = safeParse<{ status?: string; error?: string }>(event.data);
			if (payload?.error) error = payload.error;
			state = 'ended';
			source?.close();
			source = null;
		});
		source.onerror = async () => {
			if (source?.readyState === EventSource.CLOSED) {
				state = 'failed';
				source.close();
				source = null;
				try {
					const fallback = await getActivityLog(nextActivityId);
					const plain = stripAnsi(fallback || '').trimEnd();
					const text = plain ? plain.split(/\r?\n/) : [];
					applyBuffered(text);
					state = 'ended';
				} catch (reason) {
					error = reason instanceof Error ? reason.message : 'Could not load the final log.';
				}
			} else {
				state = 'connecting';
			}
		};
	},
	close() {
		if (source) {
			source.close();
			source = null;
		}
		activityId = null;
		state = 'idle';
		error = null;
		lines = [];
		seenEventIds.clear();
		truncated = false;
	}
};
