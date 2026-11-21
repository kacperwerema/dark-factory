import React from 'react';
import {AbsoluteFill, spring, useCurrentFrame, useVideoConfig} from 'remotion';
import {Media} from '../parts/Media';
import {Title} from '../parts/Title';
import {Reel} from '../Reel';
import {Plan, Scene} from '../types';

// AppDemo: a hook, then each step's screen recording in a phone frame
// with the step's text above it, then the call to action.
export const AppDemo: React.FC<Plan> = (plan) => (
	<Reel
		plan={plan}
		scene={(s) =>
			s.role === 'step' ? (
				<Step plan={plan} scene={s} />
			) : (
				<AbsoluteFill>
					<Media clip={s.clips?.[0]} background={plan.brand.colors.background} />
					<Title plan={plan} text={s.text} top={360} size={96} />
				</AbsoluteFill>
			)
		}
	/>
);

const Step: React.FC<{plan: Plan; scene: Scene}> = ({plan, scene}) => {
	const frame = useCurrentFrame();
	const {fps} = useVideoConfig();
	const rise = spring({frame, fps, config: {damping: 14}});
	const app = scene.clips?.find((c) => c.source === 'app-recording') ?? scene.clips?.[0];
	return (
		<AbsoluteFill style={{background: plan.brand.colors.background}}>
			<Title plan={plan} text={scene.text} top={250} size={64} />
			<div
				style={{
					position: 'absolute',
					left: 245,
					top: 400,
					width: 590,
					height: 820,
					borderRadius: 56,
					overflow: 'hidden',
					border: `10px solid ${plan.brand.colors.primary}`,
					transform: `translateY(${(1 - rise) * 120}px)`,
					boxShadow: '0 30px 80px rgba(0,0,0,0.5)',
				}}
			>
				<Media clip={app} background="#000" />
			</div>
		</AbsoluteFill>
	);
};
