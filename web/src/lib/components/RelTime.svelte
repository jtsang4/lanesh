<script lang="ts">
	import { clock } from '#lib/state/clock.svelte.ts';
	import { absoluteTime, relativeTime } from '#lib/format.ts';

	let { time, prefix }: { time: string; prefix?: (rel: string) => string } = $props();

	// Both formatters read the current locale, so they update when it changes.
	const rel = $derived(relativeTime(time, clock.now));
	const abs = $derived(absoluteTime(time));
</script>

<time datetime={time} title={abs}>{prefix ? prefix(rel) : rel}</time>

<style>
	time {
		font-variant-numeric: tabular-nums;
		white-space: nowrap;
	}
</style>
