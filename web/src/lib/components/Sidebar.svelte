<script lang="ts">
	import { onMount } from 'svelte';
	import BackupNav from './BackupNav.svelte';
	import ConnectionStatus from './ConnectionStatus.svelte';
	import { connection } from '$lib/stores/connection.svelte';

	interface Props {
		open: boolean;
		onClose: () => void;
	}

	let { open, onClose }: Props = $props();
	let mobile = $state(false);
	let aside = $state<HTMLElement | null>(null);

	onMount(() => {
		const media = window.matchMedia('(max-width: 767px)');
		const update = () => (mobile = media.matches);
		update();
		media.addEventListener('change', update);
		return () => media.removeEventListener('change', update);
	});

	$effect(() => {
		if (!open || !mobile) return;
		requestAnimationFrame(() => {
			aside
				?.querySelector<HTMLElement>('a, button, input, [tabindex]:not([tabindex="-1"])')
				?.focus();
		});
	});

	function close() {
		onClose();
		if (mobile) {
			requestAnimationFrame(() => {
				document.querySelector<HTMLElement>('[aria-label="Open navigation"]')?.focus();
			});
		}
	}

	function handleKeydown(event: KeyboardEvent) {
		if (!open || !mobile) return;
		if (event.key === 'Escape') {
			event.preventDefault();
			close();
			return;
		}
		if (event.key !== 'Tab' || !aside) return;
		const focusable = [
			...aside.querySelectorAll<HTMLElement>(
				'a[href], button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])'
			)
		];
		if (focusable.length === 0) return;
		const first = focusable[0];
		const last = focusable[focusable.length - 1];
		if (event.shiftKey && document.activeElement === first) {
			event.preventDefault();
			last.focus();
		} else if (!event.shiftKey && document.activeElement === last) {
			event.preventDefault();
			first.focus();
		}
	}
</script>

<svelte:window onkeydown={handleKeydown} />

{#if open}
	<button
		class="fixed inset-0 z-40 bg-backdrop/60 md:hidden"
		aria-label="Close navigation"
		onclick={close}
	></button>
{/if}

<aside
	bind:this={aside}
	class="shell-sidebar flex flex-col border-r border-outline bg-surface-raised"
	data-open={open}
	inert={mobile && !open}
	aria-hidden={mobile && !open}
	role={mobile && open ? 'dialog' : undefined}
	aria-modal={mobile && open ? 'true' : undefined}
	aria-label="Primary navigation"
>
	<BackupNav onNavigate={close} />
	<div class="border-t border-outline px-3">
		<ConnectionStatus state={connection.state} />
	</div>
</aside>
