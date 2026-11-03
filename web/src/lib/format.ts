import cronstrue from 'cronstrue';
import type { ActivityStatus, Trigger } from './api/types';

export function relativeShort(value: string | Date | null, now = Date.now()): string {
	if (!value) return '—';
	const timestamp = value instanceof Date ? value.getTime() : Date.parse(value);
	if (Number.isNaN(timestamp)) return '—';
	const delta = timestamp - now;
	const seconds = Math.floor(Math.abs(delta) / 1000);
	const suffix = delta < 0 ? 'ago' : '';
	const prefix = delta > 0 ? 'in ' : '';

	if (seconds < 5) return delta > 0 ? 'in a moment' : 'just now';
	if (seconds < 60) return `${prefix}${seconds}s ${suffix}`.trim();
	const minutes = Math.floor(seconds / 60);
	if (minutes < 60) return `${prefix}${minutes}m ${suffix}`.trim();
	const hours = Math.floor(minutes / 60);
	if (hours < 48) return `${prefix}${hours}h ${suffix}`.trim();
	const days = Math.floor(hours / 24);
	return `${prefix}${days}d ${suffix}`.trim();
}

export function formatDuration(milliseconds: number | null | undefined): string {
	if (milliseconds == null || !Number.isFinite(milliseconds)) return '—';
	const seconds = Math.max(0, Math.floor(milliseconds / 1000));
	if (seconds < 60) return `${seconds}s`;
	const minutes = Math.floor(seconds / 60);
	const remainingSeconds = seconds % 60;
	if (minutes < 60) return remainingSeconds ? `${minutes}m ${remainingSeconds}s` : `${minutes}m`;
	const hours = Math.floor(minutes / 60);
	const remainingMinutes = minutes % 60;
	return remainingMinutes ? `${hours}h ${remainingMinutes}m` : `${hours}h`;
}

export function formatDateTime(value: string | null): string {
	if (!value) return '—';
	const date = new Date(value);
	return Number.isNaN(date.getTime())
		? '—'
		: new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(date);
}

export function triggerLabel(trigger: Trigger): string {
	return trigger === 'schedule' ? 'Scheduled' : 'Manual';
}

export function statusLabel(status: ActivityStatus | 'never'): string {
	switch (status) {
		case 'running':
			return 'Running';
		case 'success':
			return 'Success';
		case 'error':
			return 'Failed';
		default:
			return 'Never run';
	}
}

export function cronText(cron: string): string {
	try {
		return cronstrue.toString(cron);
	} catch {
		return cron;
	}
}

export function redactUrl(value: string | null | undefined): string {
	if (!value) return '—';
	const input = value.trim();
	if (!input) return '—';

	try {
		const parsed = new URL(input);
		if (parsed.username || parsed.password) {
			parsed.username = '******';
			parsed.password = '';
			return parsed.toString();
		}
		return input;
	} catch {
		return input
			.replace(/\/\/([^@/:]+):([^@/]+)@/g, '//******@')
			.replace(/\/\/([^@/]+)@/g, '//******@');
	}
}

export function stripAnsi(value: string): string {
	return value.replace(/\u001B\[[0-9;]*m/g, '').replace(/\u001B\[[0-9;]*[A-Za-z]/g, '');
}
