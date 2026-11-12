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

export function nextRunUpdateDelay(value: string | null, now = Date.now()): number | null {
	if (!value) return null;
	const timestamp = Date.parse(value);
	if (Number.isNaN(timestamp)) return null;

	const remaining = timestamp - now;
	const distance = Math.abs(remaining);
	const minute = 60_000;
	const hour = 60 * minute;
	const interval = distance < minute ? 1_000 : distance < hour ? minute : hour;

	if (remaining > 0 && remaining <= minute) return 1_000;
	if (remaining > hour) return Math.max(1_000, Math.min(interval, remaining - hour));
	if (remaining > minute) return Math.max(1_000, Math.min(interval, remaining - minute));
	return interval;
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

export function formatBytes(bytes: number | null | undefined): string {
	if (bytes == null || !Number.isFinite(bytes) || bytes < 0) return '—';
	const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
	let value = bytes;
	let unit = 0;
	while (value >= 1024 && unit < units.length - 1) {
		value /= 1024;
		unit += 1;
	}
	const digits = unit === 0 || value >= 100 ? 0 : value >= 10 ? 1 : 2;
	return `${value.toFixed(digits)} ${units[unit]}`;
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
		case 'warning':
			return 'Warning';
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
