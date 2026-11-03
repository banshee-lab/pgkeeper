import { serverNow } from '$lib/api/client';

let now = $state(serverNow());
let timer: ReturnType<typeof setInterval> | undefined;

export const clock = {
	get now() {
		return now;
	},
	start() {
		if (timer) return;
		now = serverNow();
		timer = setInterval(() => {
			now = serverNow();
		}, 30_000);
	},
	stop() {
		if (!timer) return;
		clearInterval(timer);
		timer = undefined;
	}
};
