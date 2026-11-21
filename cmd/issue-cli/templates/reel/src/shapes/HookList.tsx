import React from 'react';
import {AbsoluteFill} from 'remotion';
import {Media} from '../parts/Media';
import {Title} from '../parts/Title';
import {Reel} from '../Reel';
import {Plan} from '../types';

// HookList: a hook, then 2-6 items, each numbered over its clip, then the
// call to action ("3 things you...").
export const HookList: React.FC<Plan> = (plan) => {
	const items = plan.scenes.filter((s) => s.role === 'item');
	return (
		<Reel
			plan={plan}
			scene={(s) => {
				const n = items.indexOf(s) + 1;
				return (
					<AbsoluteFill>
						<Media clip={s.clips?.[0]} background={plan.brand.colors.background} />
						{s.role === 'item' ? (
							<div
								style={{
									position: 'absolute',
									top: 260,
									left: 90,
									width: 120,
									height: 120,
									borderRadius: 60,
									background: plan.brand.colors.primary,
									color: plan.brand.colors.text,
									fontFamily: plan.brand.font ?? 'sans-serif',
									fontWeight: 900,
									fontSize: 72,
									display: 'flex',
									alignItems: 'center',
									justifyContent: 'center',
								}}
							>
								{n}
							</div>
						) : null}
						<Title plan={plan} text={s.text} top={s.role === 'hook' ? 360 : 410} size={s.role === 'hook' ? 96 : 76} />
					</AbsoluteFill>
				);
			}}
		/>
	);
};
