export type ConnectionState = 'connecting' | 'connected' | 'disconnected';

let state = $state<ConnectionState>('connecting');

export const connection = {
	get state() {
		return state;
	},
	setState(next: ConnectionState) {
		state = next;
	}
};
