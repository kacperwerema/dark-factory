import React from 'react';
import {useCurrentFrame, useVideoConfig} from 'remotion';
import {Plan} from '../types';

// Captions draws the cues the kit computed, each in its own box: QA checks
// those boxes against the platforms' safe zones and the cue times against
// the words, so draw them where and when the plan says.
export const Captions: React.FC<{plan: Plan}> = ({plan}) => {
	const frame = useCurrentFrame();
	const {fps} = useVideoConfig();
	const t = frame / fps;
	const cue = (plan.cues ?? []).find((c) => t >= c.start && t < c.end);
	if (!cue) {
		return null;
	}
	const {x, y, w, h} = cue.box;
	return (
		<div
			style={{
				position: 'absolute',
				left: x,
				top: y,
				width: w,
				height: h,
				display: 'flex',
				alignItems: 'center',
				justifyContent: 'center',
				textAlign: 'center',
				boxSizing: 'border-box',
				padding: 24,
				borderRadius: 24,
				background: 'rgba(0,0,0,0.45)',
				color: plan.brand.colors.text,
				fontFamily: plan.brand.font ?? 'sans-serif',
				fontWeight: 800,
				fontSize: plan.captions.font_size,
				lineHeight: 1.25,
			}}
		>
			{cue.text}
		</div>
	);
};
