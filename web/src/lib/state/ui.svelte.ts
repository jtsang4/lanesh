/** Small pieces of UI state shared between the shell and pages. */
class Ui {
	createLaneOpen = $state(false);
	navOpen = $state(false);
}

export const ui = new Ui();
