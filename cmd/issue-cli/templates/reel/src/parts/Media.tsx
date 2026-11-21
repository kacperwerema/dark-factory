import React from 'react';
import {AbsoluteFill, Img, OffthreadVideo, staticFile, useVideoConfig} from 'remotion';
import {Clip} from '../types';

const isImage = (src: string) => /\.(png|jpe?g|webp|gif|svg)$/i.test(src);

// Media fills its box with one clip: a video (muted: the kit lays the
// sound) or a still. With no clip it shows the brand background.
export const Media: React.FC<{clip?: Clip; background: string; style?: React.CSSProperties}> = ({clip, background, style}) => {
	const {fps} = useVideoConfig();
	if (!clip) {
		return <AbsoluteFill style={{background, ...style}} />;
	}
	const fill: React.CSSProperties = {width: '100%', height: '100%', objectFit: 'cover'};
	return (
		<AbsoluteFill style={{background, ...style}}>
			{isImage(clip.src) ? (
				<Img src={staticFile(clip.src)} style={fill} />
			) : (
				<OffthreadVideo
					src={staticFile(clip.src)}
					muted
					style={fill}
					startFrom={clip.start ? Math.round(clip.start * fps) : undefined}
					endAt={clip.end ? Math.round(clip.end * fps) : undefined}
				/>
			)}
			{clip.ai_generated ? <AiLabel /> : null}
		</AbsoluteFill>
	);
};

// AiLabel marks AI-made footage on screen (the platforms' labels are set
// when publishing; this one travels with the file).
const AiLabel: React.FC = () => (
	<div
		style={{
			position: 'absolute',
			top: 240,
			left: 70,
			padding: '6px 14px',
			borderRadius: 8,
			background: 'rgba(0,0,0,0.55)',
			color: '#fff',
			fontSize: 28,
			fontFamily: 'sans-serif',
		}}
	>
		AI
	</div>
);
