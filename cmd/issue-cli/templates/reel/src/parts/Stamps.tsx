import React from 'react';
import {interpolate, Sequence, useCurrentFrame} from 'remotion';
import {Plan, Scene, Stamp as StampT} from '../types';

const tilts = [-12, 8, -5, 10, -8, 6];

// Stamp is one word that lands on a cut: it drops in large and settles in
// its box, tilted, in the brand's accent colour (else its primary).
const Stamp: React.FC<{plan: Plan; stamp: StampT; tilt: number}> = ({plan, stamp, tilt}) => {
	const frame = useCurrentFrame();
	const scale = interpolate(frame, [0, 5], [1.8, 1], {extrapolateRight: 'clamp'});
	const opacity = interpolate(frame, [0, 3], [0, 0.92], {extrapolateRight: 'clamp'});
	const color = plan.brand.colors.accent ?? plan.brand.colors.primary;
	const {x, y, w, h} = stamp.box;
	return (
		<div style={{position: 'absolute', left: x, top: y, width: w, height: h, display: 'flex', alignItems: 'center', justifyContent: 'center'}}>
			<div
				style={{
					opacity,
					transform: `rotate(${tilt}deg) scale(${scale})`,
					border: `12px solid ${color}`,
					borderRadius: 18,
					color,
					padding: '4px 40px',
					fontFamily: plan.brand.font ?? 'sans-serif',
					fontWeight: 900,
					fontSize: 170,
					lineHeight: 1.2,
					letterSpacing: 8,
					whiteSpace: 'nowrap',
					textTransform: 'uppercase',
					background: 'rgba(255,255,255,0.12)',
				}}
			>
				{stamp.text}
			</div>
		</div>
	);
};

// Stamps draws the scene's stamps where and when the kit placed them: one
// per cut, on the spoken word when the narration says it once per cut.
export const Stamps: React.FC<{plan: Plan; scene: Scene}> = ({plan, scene}) => (
	<>
		{(scene.stamps ?? []).map((st, i) => (
			<Sequence key={i} from={st.from} durationInFrames={Math.max(1, st.frames)} layout="absolute-fill">
				<Stamp plan={plan} stamp={st} tilt={tilts[i % tilts.length]} />
			</Sequence>
		))}
	</>
);
