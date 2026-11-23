import {Plan, Scene} from './types';

// atY is a top written for the 1920 px tall 9:16 frame, on this plan's
// frame: the shapes' positions scale with the height, so one template
// renders 9:16, 4:5 and 1:1.
export const atY = (plan: Plan, px: number) => Math.round((px * plan.height) / 1920);

// belowLabel is atY, moved down below the reel's label when it has one, so
// a tag or a number in the top corner never sits on the label.
export const belowLabel = (plan: Plan, px: number) =>
	plan.label ? Math.max(atY(plan, px), plan.label.box.y + plan.label.box.h + 16) : atY(plan, px);

// showsPhone says a scene's footage is an app recording in the phone frame:
// the shapes leave its title out (the phone is the picture, the captions
// carry the words).
export const showsPhone = (s: Scene) => (s.clips ?? []).some((c) => c.source === 'app-recording');
