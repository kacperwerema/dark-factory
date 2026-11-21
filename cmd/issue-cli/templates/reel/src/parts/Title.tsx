import React from 'react';
import {interpolate, useCurrentFrame} from 'remotion';
import {Plan} from '../types';

// Title is a scene's on-screen text, kept clear of the top bar and the
// caption box.
export const Title: React.FC<{plan: Plan; text?: string; top?: number; size?: number; color?: string}> = ({plan, text, top = 320, size = 84, color}) => {
	const frame = useCurrentFrame();
	if (!text) {
		return null;
	}
	const opacity = interpolate(frame, [0, 8], [0, 1], {extrapolateRight: 'clamp'});
	const lift = interpolate(frame, [0, 8], [30, 0], {extrapolateRight: 'clamp'});
	return (
		<div
			style={{
				position: 'absolute',
				left: 90,
				width: 840,
				top,
				opacity,
				transform: `translateY(${lift}px)`,
				textAlign: 'center',
				color: color ?? plan.brand.colors.text,
				fontFamily: plan.brand.font ?? 'sans-serif',
				fontWeight: 900,
				fontSize: size,
				lineHeight: 1.1,
				textShadow: '0 4px 24px rgba(0,0,0,0.45)',
			}}
		>
			{text}
		</div>
	);
};
