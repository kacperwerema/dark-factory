import React from 'react';
import {AbsoluteFill, Img, spring, staticFile, useCurrentFrame, useVideoConfig} from 'remotion';
import {Plan} from '../types';

// Cta is the closing card: the brand, the call to action and the handle.
export const Cta: React.FC<{plan: Plan}> = ({plan}) => {
	const frame = useCurrentFrame();
	const {fps} = useVideoConfig();
	const pop = spring({frame, fps, config: {damping: 12}});
	const c = plan.brand.colors;
	return (
		<AbsoluteFill style={{background: c.primary, alignItems: 'center', justifyContent: 'center', gap: 40}}>
			{plan.brand.logo ? <Img src={staticFile(plan.brand.logo)} style={{width: 220, height: 220, objectFit: 'contain'}} /> : null}
			<div
				style={{
					transform: `scale(${0.8 + 0.2 * pop})`,
					padding: '36px 56px',
					borderRadius: 999,
					background: c.accent ?? c.background,
					color: c.text,
					fontFamily: plan.brand.font ?? 'sans-serif',
					fontWeight: 900,
					fontSize: 76,
					maxWidth: 840,
					textAlign: 'center',
				}}
			>
				{plan.cta.text}
			</div>
			{plan.cta.handle || plan.cta.url ? (
				<div style={{color: c.text, fontFamily: plan.brand.font ?? 'sans-serif', fontSize: 44, opacity: 0.9}}>
					{[plan.cta.handle, plan.cta.url].filter(Boolean).join(' · ')}
				</div>
			) : null}
		</AbsoluteFill>
	);
};
