import React from 'react';
import {AbsoluteFill, useCurrentFrame, useVideoConfig} from 'remotion';
import {Media} from './Media';
import {Clip, Plan} from '../types';

// The phone frame's bezel; the screen is the layout's phone box less it.
const bezel = 16;

// Phone shows an app recording on the screen of a plain dark phone held up
// over the scene's other footage (blurred), with a slight hand sway, so it
// reads as someone using the app rather than a glossy device mockup. Its
// box is the plan's layout.phone for this frame; with no other footage the
// backdrop is the brand's background. Taps belong in the recording itself.
export const Phone: React.FC<{plan: Plan; clip: Clip; backdrop?: Clip; playbackRate?: number; scale?: number}> = ({plan, clip, backdrop, playbackRate, scale = 1}) => {
	const frame = useCurrentFrame();
	const {fps} = useVideoConfig();
	const t = frame / fps;
	const rotate = Math.sin(t * 1.1) * 0.6 + Math.sin(t * 2.7) * 0.2;
	const x = Math.sin(t * 0.9) * 6;
	const y = Math.cos(t * 1.3) * 5;
	const box = plan.layout.phone;
	const background = plan.brand.colors.background;
	return (
		<AbsoluteFill>
			<AbsoluteFill style={{filter: 'blur(26px) brightness(0.7)', transform: 'scale(1.12)'}}>
				<Media clip={backdrop ? {...backdrop, end: undefined} : undefined} background={background} playbackRate={0.75} aiLabel={plan.label ? undefined : plan.layout.label} />
			</AbsoluteFill>
			<div
				style={{
					position: 'absolute',
					left: box.x,
					top: box.y,
					width: box.w,
					height: box.h,
					borderRadius: 68,
					background: '#16181b',
					boxShadow: '0 40px 90px rgba(0,0,0,0.55), inset 0 0 0 3px #2c2f33',
					transform: `translate(${x}px, ${y}px) rotate(${rotate}deg) scale(${scale})`,
					transformOrigin: '50% 100%',
					padding: bezel,
					boxSizing: 'border-box',
				}}
			>
				<div style={{position: 'relative', width: box.w - 2 * bezel, height: box.h - 2 * bezel, borderRadius: 54, overflow: 'hidden', background: '#fff'}}>
					<Media clip={clip} background="#fff" playbackRate={playbackRate} />
				</div>
			</div>
		</AbsoluteFill>
	);
};
