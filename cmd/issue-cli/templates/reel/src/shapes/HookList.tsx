import React from 'react';
import {AbsoluteFill} from 'remotion';
import {atY, belowLabel, showsPhone} from '../frame';
import {Clips} from '../parts/Clips';
import {Stamps} from '../parts/Stamps';
import {Title} from '../parts/Title';
import {Reel} from '../Reel';
import {Plan} from '../types';

// HookList: a hook, then 2-6 items, each numbered over its clips, then the
// call to action ("3 things you..."). A scene's clips play one after
// another; a scene with a stamp gets it on each cut.
export const HookList: React.FC<Plan> = (plan) => {
	const items = plan.scenes.filter((s) => s.role === 'item');
	return (
		<Reel
			plan={plan}
			scene={(s) => {
				const n = items.indexOf(s) + 1;
				return (
					<AbsoluteFill>
						<Clips plan={plan} scene={s} />
						{s.role === 'item' ? (
							<div
								style={{
									position: 'absolute',
									top: belowLabel(plan, 260),
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
						<Title plan={plan} text={showsPhone(s) ? undefined : s.text} top={s.role === 'hook' ? atY(plan, 360) : Math.max(atY(plan, 410), belowLabel(plan, 260) + 136)} size={s.role === 'hook' ? 96 : 76} />
						<Stamps plan={plan} scene={s} />
					</AbsoluteFill>
				);
			}}
		/>
	);
};
