<script lang="ts">
	import { Alert, Button, Heading } from '@runwisp/ui';

	let { expired = false, onSignIn }: { expired?: boolean; onSignIn: () => void } = $props();
</script>

<main class="grid min-h-screen place-items-center bg-surface px-4 py-10">
	<section class="w-full max-w-md space-y-5 border-t border-outline bg-surface-raised p-6 sm:p-8">
		<div class="flex items-center gap-3">
			<img src="/favicon.png" alt="" aria-hidden="true" class="h-10 w-10 rounded-[3px] object-contain" />
			<Heading level={1} size="xl">pgKeeper</Heading>
		</div>
		<p class="text-sm leading-6 text-on-surface-muted"></p>
		{#if expired}
			<Alert variant="warning" title="Your session expired">
				Sign in again to continue.
			</Alert>
		{/if}
		<Button variant="primary" fullWidth onclick={onSignIn}>Sign in</Button>
	</section>
</main>
