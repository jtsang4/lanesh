/** A coarse shared clock so relative times ("2 min ago") stay current. */
class Clock {
	now = $state(Date.now());

	constructor() {
		setInterval(() => (this.now = Date.now()), 15_000);
		document.addEventListener('visibilitychange', () => {
			if (!document.hidden) this.now = Date.now();
		});
	}
}

export const clock = new Clock();
