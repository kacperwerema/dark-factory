import React from 'react';
import {Plan} from '../types';

// SceneLabel is the storyboard's label, on the whole reel: a disclosure in
// the reel's own words (e.g. "Acted scene, AI voice and video") when its
// scenes are acted or its footage or voice AI-made. It sits in the plan's
// box, which QA's safe-zone check reads; with no label nothing is drawn and
// each AI clip keeps its own badge (Media).
export const SceneLabel: React.FC<{plan: Plan}> = ({plan}) => {
	if (!plan.label) {
		return null;
	}
	const {x, y, h} = plan.label.box;
	return (
		<div
			style={{
				position: 'absolute',
				left: x,
				top: y,
				height: h,
				boxSizing: 'border-box',
				padding: '8px 16px',
				borderRadius: 8,
				background: 'rgba(0,0,0,0.55)',
				color: '#fff',
				fontSize: 36,
				lineHeight: 1.25,
				whiteSpace: 'nowrap',
				fontFamily: plan.brand.font ?? 'sans-serif',
			}}
		>
			{plan.label.text}
		</div>
	);
};
