import React from 'react';
import {Sequence} from 'remotion';
import {Media} from './Media';
import {Phone} from './Phone';
import {Plan, Scene} from '../types';

// Clips plays all of a scene's clips, one after another, each in the share
// of the scene the kit gave it (from, frames) at its speed (rate: a clip
// shorter than its share plays slower instead of freezing). An app
// recording shows in the phone frame over the scene's other footage.
// aiLabelAt is where an AI clip's badge goes: the label's corner, unless the
// reel has a label (which says more).
export const aiLabelAt = (plan: Plan) => (plan.label ? undefined : plan.layout.label);

export const Clips: React.FC<{plan: Plan; scene: Scene; style?: React.CSSProperties}> = ({plan, scene, style}) => {
	const clips = scene.clips ?? [];
	const background = plan.brand.colors.background;
	if (clips.length === 0) {
		return <Media background={background} style={style} />;
	}
	const backdrop = clips.find((c) => c.source !== 'app-recording');
	return (
		<>
			{clips.map((c, i) => (
				<Sequence key={i} from={c.from ?? 0} durationInFrames={Math.max(1, c.frames ?? scene.frames)} layout="absolute-fill">
					{c.source === 'app-recording' ? (
						<Phone plan={plan} clip={c} backdrop={backdrop} playbackRate={c.rate} />
					) : (
						<Media clip={c} background={background} style={style} playbackRate={c.rate} aiLabel={aiLabelAt(plan)} />
					)}
				</Sequence>
			))}
		</>
	);
};
