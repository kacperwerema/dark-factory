import React from 'react';
import {AbsoluteFill, spring, useCurrentFrame, useVideoConfig} from 'remotion';
import {atY, showsPhone} from '../frame';
import {Clips} from '../parts/Clips';
import {Phone} from '../parts/Phone';
import {Stamps} from '../parts/Stamps';
import {Title} from '../parts/Title';
import {Reel} from '../Reel';
import {Plan, Scene} from '../types';

// AppDemo: a hook, then each step's screen recording in the phone frame
// with the step's text above it, then the call to action.
export const AppDemo: React.FC<Plan> = (plan) => (
	<Reel
		plan={plan}
		scene={(s) =>
			s.role === 'step' ? (
				<Step plan={plan} scene={s} />
			) : (
				<AbsoluteFill>
					<Clips plan={plan} scene={s} />
					<Title plan={plan} text={showsPhone(s) ? undefined : s.text} top={atY(plan, 360)} size={96} />
					<Stamps plan={plan} scene={s} />
				</AbsoluteFill>
			)
		}
	/>
);

// Step shows the step's app recording in the phone, a little smaller so
// the step's text fits above it, rising in; the scene's other footage, if
// any, is the blurred backdrop.
const Step: React.FC<{plan: Plan; scene: Scene}> = ({plan, scene}) => {
	const frame = useCurrentFrame();
	const {fps} = useVideoConfig();
	const rise = spring({frame, fps, config: {damping: 14}});
	const app = scene.clips?.find((c) => c.source === 'app-recording') ?? scene.clips?.[0];
	const backdrop = scene.clips?.find((c) => c.source !== 'app-recording');
	const top = plan.label ? plan.label.box.y + plan.label.box.h + 16 : atY(plan, 250);
	return (
		<AbsoluteFill style={{background: plan.brand.colors.background}}>
			<AbsoluteFill style={{transform: `translateY(${(1 - rise) * 120}px)`}}>
				{app ? <Phone plan={plan} clip={app} backdrop={backdrop} playbackRate={scene.clips?.length === 1 ? app.rate : 1} scale={0.86} /> : null}
			</AbsoluteFill>
			<Title plan={plan} text={scene.text} top={top} size={64} />
			<Stamps plan={plan} scene={scene} />
		</AbsoluteFill>
	);
};
