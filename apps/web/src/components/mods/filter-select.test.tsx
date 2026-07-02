import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { FilterSelect } from "./filter-select";

// Pattern-setter for component tests: render, query by role/label, fire events.
describe("FilterSelect", () => {
  it("renders the label and forwards changes", () => {
    const onChange = vi.fn();
    render(
      <FilterSelect label="Loader" value="fabric" onChange={onChange}>
        <option value="fabric">Fabric</option>
        <option value="forge">Forge</option>
      </FilterSelect>,
    );

    expect(screen.getByText("Loader")).toBeInTheDocument();
    const select = screen.getByRole("combobox");
    expect(select).toHaveValue("fabric");

    fireEvent.change(select, { target: { value: "forge" } });
    expect(onChange).toHaveBeenCalledWith("forge");
  });
});
