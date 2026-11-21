import React from 'react';
import {AbsoluteFill, Sequence} from 'remotion';
import {Captions} from './parts/Captions';
import {Cta} from './parts/Cta';
import {Plan, Scene} from './types';

// Reel lays the scenes on the plan's clock and the captions over all of
// them. Each shape passes how it draws a scene; the cta scene is shared.
export const Reel: React.FC<{plan: Plan; scene: (s: Scene, index: number) => React.ReactNode}> = ({plan, scene}) => (
	<AbsoluteFill style={{background: plan.brand.colors.background}}>
		{plan.scenes.map((s, i) => (
			<Sequence key={s.id} from={s.from} durationInFrames={Math.max(1, s.frames)} name={`${s.role}: ${s.id}`}>
				{s.role === 'cta' ? <Cta plan={plan} /> : scene(s, i)}
			</Sequence>
		))}
		<Captions plan={plan} />
	</AbsoluteFill>
);
