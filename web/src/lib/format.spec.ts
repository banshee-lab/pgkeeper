import { describe, expect, it } from 'vitest';
import {
	cronText,
	formatBytes,
	formatDuration,
	nextRunUpdateDelay,
	redactUrl,
	relativeShort,
	statusLabel,
	stripAnsi,
	triggerLabel
} from './format';

describe('relativeShort', () => {
	const now = Date.parse('2026-01-01T00:00:00Z');
	const ago = (seconds: number) => new Date(now - seconds * 1000).toISOString();
	const future = (seconds: number) => new Date(now + seconds * 1000).toISOString();

	it.each([
		[4, 'just now'],
		[59, '59s ago'],
		[60, '1m ago'],
		[59 * 60, '59m ago'],
		[3 * 60 * 60, '3h ago'],
		[2 * 24 * 60 * 60, '2d ago']
	])('formats %i seconds ago', (seconds, expected) => {
		expect(relativeShort(ago(seconds), now)).toBe(expected);
	});

	it('formats future times and missing timestamps', () => {
		expect(relativeShort(future(180), now)).toBe('in 3m');
		expect(relativeShort(null, now)).toBe('—');
	});
});

describe('nextRunUpdateDelay', () => {
	const now = Date.parse('2026-01-01T00:00:00Z');
	const at = (milliseconds: number) => new Date(now + milliseconds).toISOString();

	it('updates at the appropriate cadence and accelerates near a run', () => {
		expect(nextRunUpdateDelay(at(30_000), now)).toBe(1_000);
		expect(nextRunUpdateDelay(at(5 * 60_000), now)).toBe(60_000);
		expect(nextRunUpdateDelay(at(2 * 60 * 60_000), now)).toBe(60 * 60_000);
		expect(nextRunUpdateDelay(at(60 * 60_000 + 30_000), now)).toBe(30_000);
		expect(nextRunUpdateDelay(at(60 * 60_000 + 1), now)).toBe(1_000);
		expect(nextRunUpdateDelay(at(60_000 + 5_000), now)).toBe(5_000);
		expect(nextRunUpdateDelay(at(60_000), now)).toBe(1_000);
	});

	it('does not schedule updates for missing or invalid dates', () => {
		expect(nextRunUpdateDelay(null, now)).toBeNull();
		expect(nextRunUpdateDelay('invalid', now)).toBeNull();
	});
});

describe('display formatters', () => {
	it.each([
		[0, '0s'],
		[59_000, '59s'],
		[60_000, '1m'],
		[62_000, '1m 2s'],
		[3_660_000, '1h 1m'],
		[null, '—']
	])('formats duration %s ms', (duration, expected) => {
		expect(formatDuration(duration)).toBe(expected);
	});

	it('uses human labels for status and trigger', () => {
		expect(statusLabel('never')).toBe('Never run');
		expect(statusLabel('error')).toBe('Failed');
		expect(triggerLabel('schedule')).toBe('Scheduled');
		expect(triggerLabel('manual')).toBe('Manual');
	});

	it('redacts credentials in URLs and leaves safe URLs unchanged', () => {
		expect(redactUrl('postgres://user:password@db.example.com/app')).toBe('postgres://******@db.example.com/app');
		expect(redactUrl('s3://access:secret@backup.example.net/data')).toBe('s3://******@backup.example.net/data');
		expect(redactUrl('https://db.example.com/app')).toBe('https://db.example.com/app');
		expect(redactUrl('not a url')).toBe('not a url');
	});

	it('strips ansi codes and falls back to raw cron', () => {
		expect(stripAnsi('\u001b[31mERROR\u001b[0m')).toBe('ERROR');
		expect(cronText('0 0 * * *')).toContain('12:00');
		expect(cronText('not-a-cron')).toBe('not-a-cron');
	});
});


describe('formatBytes', () => {
	it.each([
		[0, '0 B'],
		[1023, '1023 B'],
		[1536, '1.50 KiB'],
		[10 * 1024, '10.0 KiB'],
		[250 * 1024 * 1024, '250 MiB'],
		[5 * 1024 ** 3, '5.00 GiB']
	])('formats %i bytes', (bytes, expected) => {
		expect(formatBytes(bytes)).toBe(expected);
	});

	it('handles missing and invalid values', () => {
		expect(formatBytes(null)).toBe('—');
		expect(formatBytes(undefined)).toBe('—');
		expect(formatBytes(-1)).toBe('—');
		expect(formatBytes(Number.NaN)).toBe('—');
	});
});
