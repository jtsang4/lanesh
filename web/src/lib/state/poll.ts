/** Runs `fn` now and every `ms` while the tab is visible. Returns a stop function. */
export function poll(fn: () => unknown, ms: number): () => void {
	let timer: ReturnType<typeof setTimeout> | undefined;
	let stopped = false;
	const tick = async () => {
		if (stopped) return;
		if (!document.hidden) {
			try {
				await fn();
			} catch {
				// Errors are surfaced by the caller's own state.
			}
		}
		if (!stopped) timer = setTimeout(tick, ms);
	};
	const onVisible = () => {
		if (!document.hidden && !stopped) {
			clearTimeout(timer);
			void tick();
		}
	};
	document.addEventListener('visibilitychange', onVisible);
	timer = setTimeout(tick, ms);
	return () => {
		stopped = true;
		clearTimeout(timer);
		document.removeEventListener('visibilitychange', onVisible);
	};
}
