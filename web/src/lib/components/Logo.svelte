<script lang="ts">
	let { size = 20 }: { size?: number } = $props();
</script>

<!-- Three lanes converging into one endpoint. -->
<svg width={size} height={size} viewBox="0 0 20 20" aria-hidden="true" class="logo">
	<rect width="20" height="20" rx="5.5" class="bg" />
	<path d="M5 6.5h6.5M5 10h10M5 13.5h6.5" class="lanes" />
	<circle cx="15" cy="10" r="1.6" class="dot" />
</svg>

<style>
	.logo {
		flex: none;
		display: block;
	}
	.bg {
		fill: var(--fg);
	}
	.lanes {
		stroke: var(--bg);
		stroke-width: 1.6;
		stroke-linecap: round;
		fill: none;
	}
	.dot {
		fill: var(--accent);
	}
</style>
