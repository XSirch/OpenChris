"""Inspect atlas alpha components and write frame metadata without editing PNGs.

The 72 detected frames describe eight identities per atlas, with three poses
per identity. A01-A23 use 69 frames: all of a/b and the first 21 of c. The last
three c frames (row 3, columns 3-5, zero based) are spare and remain unused.
"""

from dataclasses import dataclass
import json
from pathlib import Path

from PIL import Image


ROOT = Path(__file__).resolve().parents[1]
OFFICE = ROOT / "assets" / "office"
CELL_SIZE = 256
ROWS = 4
COLS = 6
ALPHA_THRESHOLD = 128
MIN_COMPONENT_AREA = 20
BODY_AREA_THRESHOLD = 2000
MARGIN = 2


@dataclass(frozen=True)
class Component:
    area: int
    left: int
    top: int
    right: int
    bottom: int
    sum_x: int
    sum_y: int

    def nearest_cell(self) -> tuple[int, int]:
        center_x = self.sum_x / self.area
        center_y = self.sum_y / self.area
        return min(
            ((row, col) for row in range(ROWS) for col in range(COLS)),
            key=lambda cell: (
                (center_x - (cell[1] + 0.5) * CELL_SIZE) ** 2
                + (center_y - (cell[0] + 0.5) * CELL_SIZE) ** 2
            ),
        )


def connected_components(alpha: bytes, width: int, height: int) -> list[Component]:
    """Label the full image with eight-neighbor connectivity; discard tiny noise."""
    if len(alpha) != width * height:
        raise ValueError("Alpha mask dimensions do not match its byte count")
    mask = bytearray(value >= ALPHA_THRESHOLD for value in alpha)
    components = []
    for origin in range(len(mask)):
        if not mask[origin]:
            continue
        mask[origin] = 0
        pending = [origin]
        area = sum_x = sum_y = 0
        left, top, right, bottom = width, height, 0, 0
        while pending:
            point = pending.pop()
            y, x = divmod(point, width)
            area += 1
            sum_x += x
            sum_y += y
            left, top = min(left, x), min(top, y)
            right, bottom = max(right, x + 1), max(bottom, y + 1)
            for neighbor_y in range(max(0, y - 1), min(height, y + 2)):
                for neighbor_x in range(max(0, x - 1), min(width, x + 2)):
                    neighbor = neighbor_y * width + neighbor_x
                    if mask[neighbor]:
                        mask[neighbor] = 0
                        pending.append(neighbor)
        if area >= MIN_COMPONENT_AREA:
            components.append(Component(area, left, top, right, bottom, sum_x, sum_y))
    return components


def inspect_atlas(path: Path) -> dict:
    with Image.open(path) as image:
        width, height = image.size
        if (width, height) != (COLS * CELL_SIZE, ROWS * CELL_SIZE):
            raise ValueError(f"{path.name}: expected a 1536x1024 atlas")
        if "A" not in image.getbands():
            raise ValueError(f"{path.name}: alpha channel is missing")
        alpha = image.getchannel("A").tobytes()

    components = connected_components(alpha, width, height)
    bodies = [part for part in components if part.area > BODY_AREA_THRESHOLD]
    if len(bodies) != ROWS * COLS:
        raise ValueError(f"{path.name}: expected 24 bodies, found {len(bodies)}")

    groups = {(row, col): [] for row in range(ROWS) for col in range(COLS)}
    for component in components:
        groups[component.nearest_cell()].append(component)

    frames = []
    for (row, col), parts in groups.items():
        body_count = sum(part.area > BODY_AREA_THRESHOLD for part in parts)
        if body_count != 1:
            raise ValueError(f"{path.name}: cell ({row},{col}) has {body_count} bodies")
        x = max(0, min(part.left for part in parts) - MARGIN)
        y = max(0, min(part.top for part in parts) - MARGIN)
        right = min(width, max(part.right for part in parts) + MARGIN)
        bottom = min(height, max(part.bottom for part in parts) + MARGIN)
        frames.append(
            {
                "row": row,
                "col": col,
                "x": x,
                "y": y,
                "w": right - x,
                "h": bottom - y,
                "area": sum(part.area for part in parts),
            }
        )

    for identity in range(8):
        poses = frames[identity * 3 : identity * 3 + 3]
        expected_cells = [
            (identity // 2, (identity % 2) * 3 + pose) for pose in range(3)
        ]
        if [(pose["row"], pose["col"]) for pose in poses] != expected_cells:
            raise ValueError(f"{path.name}: identity {identity + 1} lacks three poses")

    return {"width": width, "height": height, "frames": frames}


def main() -> None:
    atlases = {}
    for name in "abc":
        atlases[name] = inspect_atlas(OFFICE / f"agents-{name}.png")
        print(f"{name}: 24 frames, 8 identities, 3 poses per identity")
    output = OFFICE / "frames.json"
    output.write_text(
        json.dumps({"atlases": atlases}, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8",
    )
    print("A01-A23 use 69 frames; c row 3 columns 3-5 are spare (zero based).")
    print(f"Wrote {output.relative_to(ROOT).as_posix()}; PNGs were only read.")


if __name__ == "__main__":
    main()
