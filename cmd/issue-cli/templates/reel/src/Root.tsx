import React from 'react';
import {Composition} from 'remotion';
import {AppDemo} from './shapes/AppDemo';
import {BeforeAfter} from './shapes/BeforeAfter';
import {HookList} from './shapes/HookList';
import {Plan, samplePlan} from './types';

// One composition per shape. Size, frame rate and length come from the
// plan the kit writes (the render command in reel.json receives it), so a
// composition never needs editing for a new reel.
const compositions = [
	{id: 'HookList', component: HookList, shape: 'hook-list'},
	{id: 'BeforeAfter', component: BeforeAfter, shape: 'before-after'},
	{id: 'AppDemo', component: AppDemo, shape: 'app-demo'},
] as const;

export const Root: React.FC = () => (
	<>
		{compositions.map((c) => (
			<Composition
				key={c.id}
				id={c.id}
				component={c.component as React.FC<Plan>}
				width={1080}
				height={1920}
				fps={30}
				durationInFrames={300}
				defaultProps={samplePlan(c.shape)}
				calculateMetadata={({props}) => ({
					durationInFrames: Math.max(1, props.frames),
					fps: props.fps,
					width: props.width,
					height: props.height,
				})}
			/>
		))}
	</>
);
