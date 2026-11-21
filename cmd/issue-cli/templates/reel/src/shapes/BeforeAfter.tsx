import React from 'react';
import {AbsoluteFill, interpolate, useCurrentFrame} from 'remotion';
import {Media} from '../parts/Media';
import {Title} from '../parts/Title';
import {Reel} from '../Reel';
import {Plan, Scene} from '../types';

// BeforeAfter: a hook, the before (muted colours, a "before" tag), the
// after (a wipe in, an "after" tag), then the call to action.
export const BeforeAfter: React.FC<Plan> = (plan) => (
	<Reel plan={plan} scene={(s) => <Side plan={plan} scene={s} />} />
);

const Side: React.FC<{plan: Plan; scene: Scene}> = ({plan, scene}) => {
	const frame = useCurrentFrame();
	const after = scene.role === 'after';
	const wipe = after ? interpolate(frame, [0, 12], [100, 0], {extrapolateRight: 'clamp'}) : 0;
	const tag = scene.role === 'before' ? 'Before' : after ? 'After' : '';
	return (
		<AbsoluteFill style={{clipPath: `inset(0 0 0 ${wipe}%)`}}>
			<Media
				clip={scene.clips?.[0]}
				background={plan.brand.colors.background}
				style={scene.role === 'before' ? {filter: 'grayscale(0.7) brightness(0.85)'} : undefined}
			/>
			{tag ? (
				<div
					style={{
						position: 'absolute',
						top: 260,
						left: 90,
						padding: '12px 28px',
						borderRadius: 16,
						background: after ? plan.brand.colors.primary : 'rgba(0,0,0,0.6)',
						color: plan.brand.colors.text,
						fontFamily: plan.brand.font ?? 'sans-serif',
						fontWeight: 800,
						fontSize: 52,
					}}
				>
					{tag}
				</div>
			) : null}
			<Title plan={plan} text={scene.text} top={scene.role === 'hook' ? 360 : 400} />
		</AbsoluteFill>
	);
};
