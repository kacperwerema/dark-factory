// The plan issue-cli reel render writes (<reel>.plan.json) and passes as
// the composition's props. Go's internal/reel Plan is the source: keep the
// two in step when you change either.

export type Word = {word: string; start: number; end: number};

export type Box = {x: number; y: number; w: number; h: number};

export type Cue = {
	scene: string;
	text: string;
	start: number;
	end: number;
	from_word: number;
	to_word: number;
	// timed: a caption line timed by seconds, not words (from_word and to_word -1).
	timed?: boolean;
	box: Box;
};

export type Clip = {
	source: 'app-recording' | 'owner' | 'stock' | 'ai';
	src: string;
	file: string;
	start?: number;
	end?: number;
	ai_generated?: boolean;
	// from and frames are the clip's share of its scene; rate its speed
	// (below 1 when it is shorter than its share).
	from: number;
	frames: number;
	rate: number;
};

// Stamp is a word stamped on one cut of a scene: from and frames inside
// the scene, box where it rests (QA's safe-zone checks it).
export type Stamp = {text: string; from: number; frames: number; start: number; end: number; box: Box};

export type Scene = {
	id: string;
	role: string;
	start: number;
	duration: number;
	from: number;
	frames: number;
	text?: string;
	clips?: Clip[];
	narration?: {text: string; voice: string; start: number; seconds: number; words: Word[]};
	stamps?: Stamp[];
};

export type Plan = {
	version: number;
	shape: 'hook-list' | 'before-after' | 'app-demo';
	composition?: string;
	title: string;
	// format is the storyboard's frame: '9:16' (1080x1920), '4:5'
	// (1080x1350) or '1:1' (1080x1080); width and height are its size.
	format?: '9:16' | '4:5' | '1:1';
	width: number;
	height: number;
	fps: number;
	duration: number;
	frames: number;
	brand: {
		name: string;
		colors: {primary: string; accent?: string; text: string; background: string};
		font?: string;
		logo?: string;
	};
	cta: {text: string; url?: string; handle?: string};
	// layout is where the parts sit on this frame: the label's corner and
	// the phone frame's outer box (its screen is 16 px inside).
	layout: {label: Box; phone: Box};
	// label is the disclosure over the whole reel, when the storyboard has one.
	label?: {text: string; box: Box};
	// language is the captions' (captions.language, else the voice's).
	captions: {style: 'words' | 'lines'; position: string; font_size: number; language?: string};
	scenes: Scene[];
	cues: Cue[] | null;
};

// samplePlan is what Remotion Studio shows before a real plan is passed:
// text only, no media.
export const samplePlan = (shape: Plan['shape']): Plan => {
	const roles: Record<Plan['shape'], string[]> = {
		'hook-list': ['hook', 'item', 'item', 'item', 'cta'],
		'before-after': ['hook', 'before', 'after', 'cta'],
		'app-demo': ['hook', 'step', 'step', 'cta'],
	};
	const fps = 30;
	const scenes: Scene[] = roles[shape].map((role, i) => ({
		id: `${role}-${i}`,
		role,
		start: i * 2.5,
		duration: 2.5,
		from: i * 75,
		frames: 75,
		text: role === 'hook' ? 'A hook that stops the scroll' : role === 'cta' ? '' : `${role} ${i}`,
	}));
	return {
		version: 1,
		shape,
		title: 'Sample',
		width: 1080,
		height: 1920,
		fps,
		duration: scenes.length * 2.5,
		frames: scenes.length * 75,
		brand: {name: 'Brand', colors: {primary: '#2F6BFF', accent: '#FFB800', text: '#FFFFFF', background: '#101418'}},
		cta: {text: 'Try it free', handle: '@brand'},
		layout: {label: {x: 70, y: 240, w: 0, h: 61}, phone: {x: 254, y: 312, w: 572, h: 1201}},
		captions: {style: 'words', position: 'lower', font_size: 64},
		scenes,
		cues: [],
	};
};
